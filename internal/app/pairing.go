package app

import (
	"context"
	"errors"
	"net"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/pion/webrtc/v4"

	"github.com/tangoui/kkdesk/internal/pairing"
	"github.com/tangoui/kkdesk/internal/rendezvous"
	"github.com/tangoui/kkdesk/internal/session"
)

type mobileHostState struct {
	pending  *PairingRequest
	decision chan pairDecision
	peerKey  string
}

type PairingRequest struct {
	ID, Name, Model, Code string
	Deadline              time.Time
}

type pairDecision struct{ accept, bypass bool }

func (h *Host) deviceCode() string {
	if h.cfg.Pairing != nil {
		return h.cfg.Pairing.DeviceCode()
	}
	return rendezvous.NewCode()
}

func (h *Host) PairingAvailable() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.cfg.Pairing != nil && h.wantOn && (h.ln != nil || h.cfg.Remote.Validate() == nil && h.cfg.Remote.URL != "")
}

func (h *Host) PairingCode(now time.Time) (string, time.Time, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	identity := h.cfg.Pairing
	if identity == nil || !h.wantOn {
		return "", time.Time{}, errors.New("请先开启允许被远程控制")
	}
	var lan []string
	if h.ln != nil {
		tcp := h.ln.Addr().(*net.TCPAddr)
		addrs := h.addrs
		if tcp.IP != nil && !tcp.IP.IsUnspecified() {
			addrs = []string{tcp.IP.String()}
		}
		for _, addr := range addrs {
			lan = append(lan, net.JoinHostPort(addr, strconv.Itoa(tcp.Port)))
		}
	}
	cfg := h.cfg.Remote
	if cfg.Validate() != nil {
		cfg = rendezvous.Config{}
	}
	if len(lan) == 0 && cfg.URL == "" {
		return "", time.Time{}, errors.New("本机还没有可用的连接地址")
	}
	invite := identity.Invite(now)
	return pairing.QRURI(identity.ID(), invite.Code, identity.Fingerprint(), cfg.URL, cfg.Token, cfg.PublicKey, lan), invite.Expires, nil
}

func (h *Host) ClearPairingCode() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.cfg.Pairing != nil {
		h.cfg.Pairing.Invalidate()
	}
}

func (h *Host) PairingRequest() *PairingRequest {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.mobile.pending == nil {
		return nil
	}
	copy := *h.mobile.pending
	return &copy
}

func (h *Host) DecidePairing(id string, accept, bypass bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.mobile.pending != nil && h.mobile.pending.ID == id {
		select {
		case h.mobile.decision <- pairDecision{accept, bypass}:
		default:
		}
	}
}

func (h *Host) PairedPhones() []pairing.Phone {
	if h.cfg.Pairing == nil {
		return nil
	}
	return h.cfg.Pairing.Phones()
}

func (h *Host) UpdatePhone(key string, change func(*pairing.Phone)) error {
	if h.cfg.Pairing == nil {
		return errors.New("手机配对不可用")
	}
	return h.cfg.Pairing.Update(key, change)
}

func (h *Host) RemovePhone(key string) error {
	h.mu.Lock()
	if h.cfg.Pairing == nil {
		h.mu.Unlock()
		return errors.New("手机配对不可用")
	}
	err := h.cfg.Pairing.Remove(key)
	p := h.peer
	active := err == nil && h.mobile.peerKey == key && p != nil
	h.mu.Unlock()
	if active {
		p.CloseWithReason("已解除配对")
	}
	h.notify()
	return err
}

func (h *Host) runMobileRemote(ctx context.Context, cfg rendezvous.Config) {
	for ctx.Err() == nil {
		dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		host, err := rendezvous.RegisterMobile(dialCtx, cfg, h.cfg.Pairing)
		cancel()
		if err == nil {
			_ = host.Serve(ctx, func(call context.Context, exchange rendezvous.MobileExchange) {
				h.serveMobile(call, ctx, cfg, exchange)
			})
			host.Close()
		}
		timer := time.NewTimer(3 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-timer.C:
		}
	}
}

func (h *Host) serveMobile(ctx, listener context.Context, cfg rendezvous.Config, exchange rendezvous.MobileExchange) {
	identity := h.cfg.Pairing
	hello, err := exchange.Receive(ctx)
	if err != nil || hello.Type != "hello" || !pairing.ValidNonce(hello.Nonce) {
		return
	}
	public, ok := pairing.PublicKey(hello.PublicKey)
	if !ok {
		return
	}
	switch hello.Purpose {
	case "pair", "connect", "status", "unpair":
	default:
		return
	}
	nonce := pairing.Random()
	transcript := pairing.Transcript{Purpose: hello.Purpose, ClientNonce: hello.Nonce,
		HostNonce: nonce, ClientKey: hello.PublicKey, HostKey: identity.PublicKey()}
	send := func(msg pairing.Message) error {
		identity.Sign(transcript, &msg)
		return exchange.Send(ctx, msg)
	}
	result := func(status string) {
		_ = send(pairing.Message{Type: "result", Status: status})
	}
	phone, paired := identity.Phone(hello.PublicKey)
	challenge := pairing.Message{Type: "challenge", Nonce: nonce, PublicKey: identity.PublicKey(), Bypass: paired && phone.Bypass}
	if hello.Purpose == "pair" {
		challenge.Name, _ = os.Hostname()
		challenge.OS = runtime.GOOS
	}
	if err := send(challenge); err != nil {
		return
	}
	req, err := exchange.Receive(ctx)
	if err != nil || !transcript.Verify(hello.PublicKey, req) {
		return
	}
	if hello.Purpose == "pair" && req.Type == "request" {
		if lan, ok := exchange.(*session.MobileLAN); ok {
			// With no further messages expected, EOF cancels the prompt.
			var stop context.CancelFunc
			ctx, stop = context.WithCancel(ctx)
			defer stop()
			go func() {
				_, _ = lan.Receive(ctx)
				stop()
			}()
		}
		h.requestPairing(ctx, transcript, public, req, send)
		return
	}
	phone, paired = identity.Phone(hello.PublicKey)
	if !paired {
		result("unpaired")
		return
	}
	switch hello.Purpose {
	case "status":
		if req.Type == "request" {
			_ = send(pairing.Message{Type: "result", Status: "ok", Bypass: phone.Bypass})
		}
	case "unpair":
		if req.Type == "request" {
			if err := h.RemovePhone(hello.PublicKey); err != nil {
				result("save_failed")
				return
			}
			result("ok")
		}
	case "connect":
		if req.Type == "offer" && (req.Mode == "desktop" || req.Mode == "files") && req.SDP != "" {
			h.answerMobile(listener, cfg, exchange, hello.PublicKey, challenge.Bypass && phone.Bypass, req, send, result)
		}
	}
}

func identityPublic(store *pairing.Store) []byte {
	public, _ := pairing.PublicKey(store.PublicKey())
	return public
}

func (h *Host) requestPairing(ctx context.Context, exchange pairing.Transcript, public []byte, req pairing.Message, send func(pairing.Message) error) {
	key := exchange.ClientKey
	result := func(status string, bypass bool) error {
		return send(pairing.Message{Type: "result", Status: status, Bypass: bypass})
	}
	name, model := strings.TrimSpace(req.Name), strings.TrimSpace(req.Model)
	if name == "" || len(name) > 256 || len(model) > 256 {
		_ = result("invalid", false)
		return
	}
	h.mu.Lock()
	if !h.wantOn || h.mobile.pending != nil {
		h.mu.Unlock()
		_ = result("busy", false)
		return
	}
	if _, exists := h.cfg.Pairing.Phone(key); exists {
		h.mu.Unlock()
		_ = result("already_paired", false)
		return
	}
	now := time.Now()
	invite, err := h.cfg.Pairing.Claim(exchange, req.Code, now)
	if err != nil {
		h.mu.Unlock()
		_ = result("expired", false)
		return
	}
	verification := pairing.VerificationCode(invite.Code, identityPublic(h.cfg.Pairing), public)
	pending := &PairingRequest{ID: pairing.Random(), Name: name, Model: model, Code: verification,
		Deadline: minTime(invite.Expires, now.Add(ConfirmTimeout))}
	decision := make(chan pairDecision, 1)
	h.mobile.pending, h.mobile.decision = pending, decision
	h.mu.Unlock()
	h.notify()
	defer func() {
		h.mu.Lock()
		h.mobile.pending, h.mobile.decision = nil, nil
		h.mu.Unlock()
		h.notify()
	}()
	if err := send(pairing.Message{Type: "result", Status: "pending", Code: verification, Deadline: pending.Deadline.UnixMilli()}); err != nil {
		return
	}

	timer := time.NewTimer(time.Until(pending.Deadline))
	defer timer.Stop()
	select {
	case choice := <-decision:
		if !choice.accept {
			_ = result("rejected", false)
			return
		}
		h.mu.Lock()
		if !h.wantOn || ctx.Err() != nil || !time.Now().Before(pending.Deadline) {
			h.mu.Unlock()
			_ = result("timed_out", false)
			return
		}
		err := h.cfg.Pairing.Save(pairing.Phone{PublicKey: key, Name: name, Model: model, Bypass: choice.bypass})
		h.mu.Unlock()
		if err != nil {
			_ = result("save_failed", false)
			return
		}
		if err := result("ok", choice.bypass); err != nil {
			_ = h.RemovePhone(key)
		}
	case <-timer.C:
		_ = result("timed_out", false)
	case <-ctx.Done():
	}
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

func (h *Host) answerMobile(listener context.Context, cfg rendezvous.Config, exchange rendezvous.MobileExchange,
	key string, bypass bool, req pairing.Message, send func(pairing.Message) error, result func(string)) {
	h.mu.Lock()
	busy := h.busy
	h.mu.Unlock()
	if busy {
		result("busy")
		return
	}
	offer := webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: req.SDP}
	mode := session.Mode(req.Mode)
	var peer *session.Peer
	var answer *webrtc.SessionDescription
	var err error
	var remote context.Context
	switch ex := exchange.(type) {
	case *session.MobileLAN:
		peer, answer, err = session.AnswerMobileLAN(ex, mode, offer)
	case *rendezvous.HostMobileExchange:
		remote = listener
		peer, answer, err = session.AnswerMobileRemote(cfg, ex, mode, offer)
	default:
		return
	}
	if err != nil {
		result("failed")
		return
	}
	if _, ok := h.cfg.Pairing.Phone(key); !ok {
		peer.Close()
		result("unpaired")
		return
	}
	if err := send(pairing.Message{Type: "answer", SDP: answer.SDP}); err != nil {
		peer.Close()
		return
	}
	h.wg.Go(func() {
		ctx, cancel := context.WithTimeout(listener, 30*time.Second)
		defer cancel()
		if err := peer.WaitReady(ctx); err != nil {
			peer.Close()
			return
		}
		h.handle(peer, remote, key, bypass)
	})
}

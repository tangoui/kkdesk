package app

import (
	"context"
	"log"
	"time"

	"github.com/coder/websocket"

	"github.com/tangoui/kkdesk/internal/rendezvous"
	"github.com/tangoui/kkdesk/internal/session"
)

type remoteHostState struct {
	code   string
	cancel context.CancelFunc
	online bool
	err    string
}

func (h *Host) SetRemote(cfg rendezvous.Config) {
	h.mu.Lock()
	defer h.notify()
	defer h.mu.Unlock()
	if h.closed || h.cfg.Remote == cfg {
		return
	}
	h.stopRemoteLocked()
	h.cfg.Remote = cfg
	h.remote.code = h.deviceCode()
	if h.wantOn {
		h.startRemoteLocked()
	}
}

func (h *Host) startRemoteLocked() {
	if h.remote.cancel != nil || h.cfg.Remote.URL == "" {
		return
	}
	if err := h.cfg.Remote.Validate(); err != nil {
		h.remote.err = err.Error()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	h.remote.cancel = cancel
	cfg, code := h.cfg.Remote, h.remote.code
	h.wg.Add(1)
	go func() {
		defer h.wg.Done()
		h.serveRemote(ctx, cfg, code)
	}()
	if h.cfg.Pairing != nil {
		h.wg.Go(func() { h.runMobileRemote(ctx, cfg) })
	}
}

func (h *Host) stopRemoteLocked() {
	if h.remote.cancel != nil {
		h.remote.cancel()
		h.remote.cancel = nil
	}
	h.remote.online, h.remote.err = false, ""
	if !h.busy && h.ln == nil {
		h.phase = HostOff
	}
}

func (h *Host) remoteStatus(ctx context.Context, online bool, reason string) {
	h.mu.Lock()
	if ctx.Err() != nil {
		h.mu.Unlock()
		return
	}
	h.remote.online, h.remote.err = online, reason
	if !h.busy && h.ln == nil {
		h.phase = HostOff
		if online {
			h.phase = HostListening
		}
	}
	h.mu.Unlock()
	h.notify()
}

func (h *Host) serveRemote(ctx context.Context, cfg rendezvous.Config, code string) {
	for ctx.Err() == nil {
		dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		conn, err := rendezvous.Register(dialCtx, cfg, code)
		cancel()
		if err == nil {
			h.remoteStatus(ctx, true, "")
			err = h.remoteOffers(ctx, cfg, conn)
			_ = conn.CloseNow()
		}
		if ctx.Err() != nil {
			return
		}
		h.remoteStatus(ctx, false, err.Error())
		wait := time.NewTimer(3 * time.Second)
		select {
		case <-ctx.Done():
			wait.Stop()
			return
		case <-wait.C:
		}
	}
}

func (h *Host) remoteOffers(ctx context.Context, cfg rendezvous.Config, conn *websocket.Conn) error {
	// Keep reading control frames while ICE gathering runs for a request.
	for {
		req, err := rendezvous.ReadOffer(ctx, conn)
		if err != nil {
			return err
		}
		h.wg.Add(1)
		go func() {
			defer h.wg.Done()
			h.answerRemote(ctx, cfg, conn, req)
		}()
	}
}

func (h *Host) answerRemote(ctx context.Context, cfg rendezvous.Config, conn *websocket.Conn, req rendezvous.Offer) {
	replyCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	h.mu.Lock()
	busy := h.busy
	h.mu.Unlock()
	if busy {
		_ = rendezvous.Reply(replyCtx, conn, req.ID, nil, "对方正在处理其他连接")
		return
	}
	// A controller that proves the temporary or fixed password skips the
	// confirmation like on the LAN (when 连接前需要确认 is off); a wrong
	// password is refused like on the LAN. Without a password (an older
	// controller) the user confirms.
	h.mu.Lock()
	passwords := []string{h.password, h.cfg.Fixed}
	h.mu.Unlock()
	var auth session.RemoteAuth
	var authPW string
	req.SDP.SDP, auth, authPW = session.VerifyRemoteOfferPassword(req.SDP.SDP, passwords...)
	log.Printf("kkdesk host: cross-network request (%s, %s)", req.Mode, auth)
	if auth == session.RemoteAuthWrong {
		_ = rendezvous.Reply(replyCtx, conn, req.ID, nil, "密码错误")
		return
	}
	passwordOK := auth == session.RemoteAuthOK
	peer, answer, err := session.AnswerRemote(cfg, req)
	if err != nil {
		_ = rendezvous.Reply(replyCtx, conn, req.ID, nil, "被控端无法建立跨网连接")
		return
	}
	peer.SetAuthPassword(authPW)
	if err := rendezvous.Reply(replyCtx, conn, req.ID, answer, ""); err != nil {
		peer.Close()
		return
	}
	if err := peer.WaitReady(replyCtx); err != nil {
		peer.Close()
		return
	}
	h.handle(peer, ctx, true, passwordOK, "", "")
}

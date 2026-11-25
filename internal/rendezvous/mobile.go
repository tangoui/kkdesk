package rendezvous

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/pion/webrtc/v4"

	"github.com/tangoui/kkdesk/internal/pairing"
)

const (
	mobileTimeout = 2 * time.Minute
	mobileSlots   = 12
)

// MobileExchange carries end-to-end signed mobile messages.
type MobileExchange interface {
	Send(context.Context, pairing.Message) error
	Receive(context.Context) (pairing.Message, error)
}

type MobileGuest struct {
	conn      *websocket.Conn
	ICE       []webrtc.ICEServer
	RelayOnly bool
}

func OpenMobile(ctx context.Context, cfg Config, device string, media bool) (*MobileGuest, error) {
	action := "mobile"
	if media {
		action = "mobile_connect"
	}
	conn, msg, err := openMobile(ctx, cfg, device, action, "mobile_ready")
	if err != nil {
		return nil, err
	}
	return &MobileGuest{conn: conn, ICE: msg.ICE, RelayOnly: msg.RelayOnly}, nil
}

func MobileOnline(ctx context.Context, cfg Config, device string) (bool, error) {
	conn, msg, err := openMobile(ctx, cfg, device, "mobile_status", "mobile_status")
	if conn != nil {
		conn.CloseNow()
	}
	return msg.Online, err
}

func openMobile(ctx context.Context, cfg Config, device, action, expect string) (*websocket.Conn, message, error) {
	conn, err := cfg.dial(ctx)
	if err != nil {
		return nil, message{}, err
	}
	if err := write(ctx, conn, message{Type: action, Device: device}); err != nil {
		conn.CloseNow()
		return nil, message{}, err
	}
	msg, err := read(ctx, conn)
	if err == nil && msg.Type != expect {
		err = errors.New("无效的手机信令响应")
	}
	if err != nil {
		conn.CloseNow()
		return nil, message{}, err
	}
	return conn, msg, nil
}

func (g *MobileGuest) Close() { _ = g.conn.CloseNow() }

func (g *MobileGuest) Send(ctx context.Context, msg pairing.Message) error {
	return write(ctx, g.conn, message{Type: "mobile_data", Mobile: &msg})
}

func (g *MobileGuest) Receive(ctx context.Context) (pairing.Message, error) {
	msg, err := read(ctx, g.conn)
	if err != nil {
		return pairing.Message{}, err
	}
	if msg.Type != "mobile_data" || msg.Mobile == nil {
		return pairing.Message{}, io.EOF
	}
	return *msg.Mobile, nil
}

type MobileHost struct{ conn *websocket.Conn }

func RegisterMobile(ctx context.Context, cfg Config, identity *pairing.Store) (*MobileHost, error) {
	conn, msg, err := openMobile(ctx, cfg, identity.ID(), "mobile_register", "mobile_challenge")
	if err != nil {
		return nil, err
	}
	if msg.Mobile == nil || !pairing.ValidNonce(msg.Mobile.Nonce) {
		conn.CloseNow()
		return nil, errors.New("无效的服务器注册挑战")
	}
	proof := pairing.Message{Type: "register", PublicKey: identity.PublicKey()}
	transcript := registrationTranscript(msg.Mobile.Nonce, proof.PublicKey)
	identity.Sign(transcript, &proof)
	err = write(ctx, conn, message{Type: "mobile_proof", Mobile: &proof})
	if err == nil {
		msg, err = read(ctx, conn)
	}
	if err == nil && msg.Type != "mobile_registered" {
		err = errors.New("手机信令注册失败")
	}
	if err != nil {
		conn.CloseNow()
		return nil, err
	}
	return &MobileHost{conn: conn}, nil
}

func registrationTranscript(nonce, key string) pairing.Transcript {
	return pairing.Transcript{Purpose: "register", HostNonce: nonce, HostKey: key}
}

func (h *MobileHost) Close() { _ = h.conn.CloseNow() }

type HostMobileExchange struct {
	conn      *websocket.Conn
	connCtx   context.Context
	id        string
	input     chan pairing.Message
	ICE       []webrtc.ICEServer
	RelayOnly bool
}

func (e *HostMobileExchange) Send(ctx context.Context, msg pairing.Message) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// A canceled write can close the socket shared by all requests.
	return writeWithTimeout(e.connCtx, e.conn, message{Type: "mobile_data", ID: e.id, Mobile: &msg})
}

func (e *HostMobileExchange) Receive(ctx context.Context) (pairing.Message, error) {
	select {
	case msg := <-e.input:
		return msg, nil
	case <-ctx.Done():
		// The relay sends mobile_end after forwarding all data. Preserve any
		// receipt already queued before that notification was handled.
		select {
		case msg := <-e.input:
			return msg, nil
		default:
			return pairing.Message{}, ctx.Err()
		}
	}
}

func (h *MobileHost) Serve(ctx context.Context, handle func(context.Context, MobileExchange)) error {
	type request struct {
		exchange *HostMobileExchange
		cancel   context.CancelFunc
	}
	ctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	defer func() {
		cancel()
		wg.Wait()
	}()
	var mu sync.Mutex
	requests := make(map[string]request)
	for {
		msg, err := read(ctx, h.conn)
		if err != nil {
			return err
		}
		mu.Lock()
		active, exists := requests[msg.ID]
		switch msg.Type {
		case "mobile_begin":
			if !exists && msg.ID != "" && len(requests) < mobileSlots {
				e := &HostMobileExchange{conn: h.conn, connCtx: ctx, id: msg.ID, input: make(chan pairing.Message, 4),
					ICE: msg.ICE, RelayOnly: msg.RelayOnly}
				call, stop := context.WithTimeout(ctx, mobileTimeout)
				requests[msg.ID] = request{exchange: e, cancel: stop}
				wg.Go(func() {
					defer stop()
					handle(call, e)
					_ = writeWithTimeout(ctx, h.conn, message{Type: "mobile_end", ID: e.id})
					mu.Lock()
					delete(requests, e.id)
					mu.Unlock()
				})
			}
		case "mobile_data":
			if exists && msg.Mobile != nil {
				select {
				case active.exchange.input <- *msg.Mobile:
				default:
					active.cancel()
				}
			}
		case "mobile_end":
			if exists {
				active.cancel()
			}
		}
		mu.Unlock()
	}
}

type mobileRegistration struct {
	conn     *websocket.Conn
	ctx      context.Context
	done     chan struct{}
	requests map[string]chan message // guarded by Server.mu
}

func validDeviceID(id string) bool {
	data, err := base64.RawURLEncoding.Strict().DecodeString(id)
	return err == nil && len(data) == 16 && pairing.Encode(data) == id
}

func (s *Server) handleMobile(ctx context.Context, conn *websocket.Conn, msg message) bool {
	switch msg.Type {
	case "mobile_register", "mobile_status", "mobile", "mobile_connect":
	default:
		return false
	}
	if !validDeviceID(msg.Device) {
		sendError(ctx, conn, "电脑识别码格式不正确")
		return true
	}
	switch msg.Type {
	case "mobile_register":
		s.registerMobile(ctx, conn, msg.Device)
	case "mobile_status":
		s.mu.Lock()
		online := s.mobile[msg.Device] != nil
		s.mu.Unlock()
		_ = writeWithTimeout(ctx, conn, message{Type: "mobile_status", Online: online})
	default:
		s.forwardMobile(ctx, conn, msg.Device, msg.Type == "mobile_connect")
	}
	return true
}

func (s *Server) registerMobile(ctx context.Context, conn *websocket.Conn, device string) {
	nonce := pairing.Random()
	if err := writeWithTimeout(ctx, conn, message{Type: "mobile_challenge", Mobile: &pairing.Message{Type: "challenge", Nonce: nonce}}); err != nil {
		return
	}
	authCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	msg, err := read(authCtx, conn)
	cancel()
	if err != nil || msg.Type != "mobile_proof" || msg.Mobile == nil {
		return
	}
	proof := *msg.Mobile
	public, ok := pairing.PublicKey(proof.PublicKey)
	if !ok || pairing.DeviceID(public) != device ||
		!registrationTranscript(nonce, proof.PublicKey).Verify(proof.PublicKey, proof) {
		sendError(ctx, conn, "电脑身份校验失败")
		return
	}
	ctx, stop := context.WithCancel(ctx)
	defer stop()
	h := &mobileRegistration{conn: conn, ctx: ctx, done: make(chan struct{}), requests: make(map[string]chan message)}
	s.mu.Lock()
	if s.mobile[device] != nil {
		s.mu.Unlock()
		sendError(ctx, conn, "电脑已上线")
		return
	}
	// Hold the lock until the registration acknowledgement is on the wire.
	err = writeWithTimeout(ctx, conn, message{Type: "mobile_registered"})
	if err == nil {
		s.mobile[device] = h
	}
	s.mu.Unlock()
	if err != nil {
		return
	}
	defer func() {
		s.mu.Lock()
		delete(s.mobile, device)
		close(h.done)
		s.mu.Unlock()
	}()
	go heartbeat(ctx, conn)
	for {
		msg, err := read(ctx, conn)
		if err != nil {
			return
		}
		if msg.Type != "mobile_data" && msg.Type != "mobile_end" {
			return
		}
		s.mu.Lock()
		if out := h.requests[msg.ID]; out != nil {
			select {
			case out <- msg:
			default:
			}
		}
		s.mu.Unlock()
	}
}

func (s *Server) forwardMobile(ctx context.Context, conn *websocket.Conn, device string, media bool) {
	ctx, cancel := context.WithTimeout(ctx, mobileTimeout)
	defer cancel()
	id := pairing.Random()
	out := make(chan message, 8)
	s.mu.Lock()
	h := s.mobile[device]
	if h == nil || len(h.requests) >= mobileSlots {
		s.mu.Unlock()
		reason := "被控设备未上线"
		if h != nil {
			reason = "被控设备正在处理其他请求"
		}
		sendError(ctx, conn, reason)
		return
	}
	h.requests[id] = out
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(h.requests, id)
		s.mu.Unlock()
	}()
	begin, ready := message{Type: "mobile_begin", ID: id}, message{Type: "mobile_ready"}
	if media {
		var err error
		begin.ICE, err = s.ice(id + "-host")
		if err == nil {
			ready.ICE, err = s.mobileGuestICE(id + "-guest")
		}
		if err != nil {
			sendError(ctx, conn, "无法签发中继凭据")
			return
		}
		begin.RelayOnly, ready.RelayOnly = s.RelayOnly, s.RelayOnly
	}
	// All writes to the multiplexed host socket use its registration context.
	if err := writeWithTimeout(h.ctx, h.conn, begin); err != nil {
		sendError(ctx, conn, "被控设备已离线")
		return
	}
	end := message{Type: "mobile_end", ID: id}
	if err := writeWithTimeout(ctx, conn, ready); err != nil {
		_ = writeWithTimeout(h.ctx, h.conn, end)
		return
	}
	inputDone := make(chan struct{})
	go func() {
		defer close(inputDone)
		for {
			msg, err := read(ctx, conn)
			if err != nil || ctx.Err() != nil || msg.Type != "mobile_data" || msg.Mobile == nil {
				return
			}
			if err := writeWithTimeout(h.ctx, h.conn, message{Type: "mobile_data", ID: id, Mobile: msg.Mobile}); err != nil {
				return
			}
		}
	}()
	defer func() {
		cancel()
		<-inputDone
		_ = writeWithTimeout(h.ctx, h.conn, end)
	}()
	for {
		select {
		case msg := <-out:
			if msg.Type == "mobile_end" || writeWithTimeout(ctx, conn, message{Type: "mobile_data", Mobile: msg.Mobile}) != nil {
				return
			}
		case <-inputDone:
			return
		case <-h.done:
			sendError(ctx, conn, "被控设备已离线")
			return
		case <-ctx.Done():
			return
		}
	}
}

func writeWithTimeout(ctx context.Context, conn *websocket.Conn, msg message) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return write(ctx, conn, msg)
}

package rendezvous

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/pion/turn/v4"
	"github.com/pion/webrtc/v4"
)

type Server struct {
	RelayOnly           bool // configure before serving
	token               [32]byte
	turnSecret          string
	credentialTTL       time.Duration
	stunURLs            []string
	turnURLs            []string
	mobileGuestTURNURLs []string
	slots               chan struct{}

	mu      sync.Mutex
	devices map[string]*registration
	mobile  map[string]*mobileRegistration
}

type registration struct {
	conn    *websocket.Conn
	ctx     context.Context
	ready   chan struct{}
	done    chan struct{}
	pending *pendingPair // guarded by Server.mu
}

type pendingPair struct {
	id     string
	answer chan message
}

type ServerOptions struct {
	MaxConnections      int
	CredentialTTL       time.Duration
	MobileGuestTURNURLs []string // when set, replaces the mobile guest ICE list
}

func DefaultServerOptions() ServerOptions {
	return ServerOptions{MaxConnections: 2048, CredentialTTL: 8 * time.Hour}
}

func NewServer(token, turnSecret string, iceURLs []string) (*Server, error) {
	return NewServerWithOptions(token, turnSecret, iceURLs, DefaultServerOptions())
}

func NewServerWithOptions(token, turnSecret string, iceURLs []string, opts ServerOptions) (*Server, error) {
	if len(token) < 32 || len(token) > 256 || strings.ContainsAny(token, " \t\r\n") || len(turnSecret) < 32 {
		return nil, errors.New("signal and TURN secrets must use at least 32 characters")
	}
	if opts.MaxConnections < 1 || opts.CredentialTTL < time.Second {
		return nil, errors.New("invalid signaling connection limit or TURN credential lifetime")
	}
	s := &Server{token: sha256.Sum256([]byte("Bearer " + token)), turnSecret: turnSecret,
		credentialTTL: opts.CredentialTTL, slots: make(chan struct{}, opts.MaxConnections),
		devices: make(map[string]*registration), mobile: make(map[string]*mobileRegistration)}
	for _, u := range iceURLs {
		switch {
		case strings.HasPrefix(u, "stun:"):
			s.stunURLs = append(s.stunURLs, u)
		case strings.HasPrefix(u, "turn:"), strings.HasPrefix(u, "turns:"):
			s.turnURLs = append(s.turnURLs, u)
		default:
			return nil, errors.New("invalid STUN/TURN URL")
		}
	}
	if len(s.turnURLs) == 0 {
		return nil, errors.New("at least one TURN URL is required")
	}
	for _, u := range opts.MobileGuestTURNURLs {
		if !strings.HasPrefix(u, "turn:") {
			return nil, errors.New("mobile guest TURN URLs must use the turn scheme")
		}
	}
	s.mobileGuestTURNURLs = slices.Clone(opts.MobileGuestTURNURLs)
	return s, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/healthz" {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
		return
	}
	if r.URL.Path != path {
		http.NotFound(w, r)
		return
	}
	presented := sha256.Sum256([]byte(r.Header.Get("Authorization")))
	if subtle.ConstantTimeCompare(s.token[:], presented[:]) != 1 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	default:
		http.Error(w, "server busy", http.StatusServiceUnavailable)
		return
	}
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer conn.CloseNow()
	conn.SetReadLimit(readLimit)
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	msg, err := read(ctx, conn)
	cancel()
	if err != nil {
		return
	}
	if s.handleMobile(r.Context(), conn, msg) {
		return
	}
	if !IsCode(msg.Code) {
		sendError(r.Context(), conn, "设备码格式不正确")
		return
	}
	code := NormalizeCode(msg.Code)
	switch msg.Type {
	case "register":
		s.register(r.Context(), conn, code)
	case "join":
		s.join(r.Context(), conn, code)
	default:
		sendError(r.Context(), conn, "未知的跨网信令操作")
	}
}

func sendError(ctx context.Context, conn *websocket.Conn, reason string) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	_ = write(ctx, conn, message{Type: "error", Error: reason})
}

func (s *Server) register(ctx context.Context, conn *websocket.Conn, code string) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	h := &registration{conn: conn, ctx: ctx, ready: make(chan struct{}), done: make(chan struct{})}
	s.mu.Lock()
	if s.devices[code] != nil {
		s.mu.Unlock()
		sendError(ctx, conn, "设备码已上线")
		return
	}
	s.devices[code] = h
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.devices, code)
		close(h.done)
		s.mu.Unlock()
	}()

	if err := writeWithTimeout(ctx, conn, message{Type: "registered"}); err != nil {
		return
	}
	close(h.ready)
	go heartbeat(ctx, conn)
	for {
		msg, err := read(ctx, conn)
		if err != nil {
			return
		}
		if msg.Type != "answer" {
			return
		}
		s.mu.Lock()
		pair := h.pending
		if pair != nil && pair.id == msg.ID {
			select {
			case pair.answer <- msg:
			default:
			}
		}
		s.mu.Unlock()
	}
}

func heartbeat(ctx context.Context, conn *websocket.Conn) {
	tick := time.NewTicker(15 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			err := conn.Ping(pingCtx)
			cancel()
			if err != nil {
				_ = conn.CloseNow()
				return
			}
		}
	}
}

func (s *Server) join(ctx context.Context, conn *websocket.Conn, code string) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	pair := &pendingPair{id: rand.Text(), answer: make(chan message, 1)}
	s.mu.Lock()
	h := s.devices[code]
	if h == nil || h.pending != nil {
		s.mu.Unlock()
		reason := "被控设备未上线"
		if h != nil {
			reason = "被控设备正在处理另一个连接请求"
		}
		sendError(ctx, conn, reason)
		return
	}
	h.pending = pair
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		h.pending = nil
		s.mu.Unlock()
	}()

	select {
	case <-h.ready:
	case <-h.done:
		sendError(ctx, conn, "被控设备已离线")
		return
	case <-ctx.Done():
		return
	}
	guestICE, err := s.ice(pair.id + "-guest")
	if err != nil {
		sendError(ctx, conn, "无法签发中继凭据")
		return
	}
	if err := write(ctx, conn, message{Type: "ready", ICE: guestICE, RelayOnly: s.RelayOnly}); err != nil {
		return
	}
	offer, err := read(ctx, conn)
	if err != nil {
		return
	}
	if offer.Type != "offer" || offer.SDP == nil || offer.SDP.Type != webrtc.SDPTypeOffer ||
		(offer.Mode != "desktop" && offer.Mode != "files") {
		sendError(ctx, conn, "无效的跨网连接请求")
		return
	}
	guestCtx := conn.CloseRead(ctx)
	hostICE, err := s.ice(pair.id + "-host")
	if err != nil {
		sendError(ctx, conn, "无法签发中继凭据")
		return
	}
	forward := message{Type: "offer", ID: pair.id, Mode: offer.Mode, SDP: offer.SDP, ICE: hostICE, RelayOnly: s.RelayOnly}
	if guestCtx.Err() != nil {
		return
	}
	if err := writeWithTimeout(h.ctx, h.conn, forward); err != nil {
		sendError(ctx, conn, "被控设备已离线")
		return
	}
	select {
	case answer := <-pair.answer:
		if answer.Error != "" {
			sendError(ctx, conn, answer.Error)
		} else if answer.SDP != nil && answer.SDP.Type == webrtc.SDPTypeAnswer {
			_ = write(ctx, conn, message{Type: "answer", SDP: answer.SDP})
		} else {
			sendError(ctx, conn, "无效的跨网连接应答")
		}
	case <-h.done:
		sendError(ctx, conn, "被控设备已离线")
	case <-guestCtx.Done():
	}
}

func (s *Server) ice(id string) ([]webrtc.ICEServer, error) {
	return s.issueICE(id, s.stunURLs, s.turnURLs)
}

func (s *Server) mobileGuestICE(id string) ([]webrtc.ICEServer, error) {
	if len(s.mobileGuestTURNURLs) == 0 {
		return s.ice(id)
	}
	return s.issueICE(id, nil, s.mobileGuestTURNURLs)
}

func (s *Server) issueICE(id string, stunURLs, turnURLs []string) ([]webrtc.ICEServer, error) {
	user, credential, err := turn.GenerateLongTermTURNRESTCredentials(s.turnSecret, id, s.credentialTTL)
	if err != nil {
		return nil, err
	}
	var servers []webrtc.ICEServer
	if len(stunURLs) != 0 {
		servers = append(servers, webrtc.ICEServer{URLs: stunURLs})
	}
	return append(servers, webrtc.ICEServer{URLs: turnURLs, Username: user, Credential: credential}), nil
}

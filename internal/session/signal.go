package session

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/pion/webrtc/v4"
)

// ErrBadPassword is returned by Dial when the host rejects the password, and
// by Host.Accept when a client fails the challenge. The host keeps listening.
var ErrBadPassword = errors.New("session: bad password")

// signalTimeout bounds the whole TCP signaling exchange when the caller's
// context has no earlier deadline.
const signalTimeout = 30 * time.Second

// sigMsg is one JSON line of the signaling protocol:
//
//	host → client  {"nonce": base64}
//	client → host  {"mac": base64(HMAC-SHA256(password, nonce))}
//	host → client  {"ok": true} | {"ok": false, "error": "bad password"}
//	client → host  {"sdp": offer}
//	host → client  {"sdp": answer}
type sigMsg struct {
	Nonce string                     `json:"nonce,omitempty"`
	MAC   string                     `json:"mac,omitempty"`
	OK    *bool                      `json:"ok,omitempty"`
	Error string                     `json:"error,omitempty"`
	SDP   *webrtc.SessionDescription `json:"sdp,omitempty"`
}

type sigConn struct {
	conn net.Conn
	enc  *json.Encoder
	dec  *json.Decoder
}

func newSigConn(ctx context.Context, c net.Conn) (*sigConn, func()) {
	deadline := time.Now().Add(signalTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = c.SetDeadline(deadline)
	stop := context.AfterFunc(ctx, func() { _ = c.SetDeadline(time.Now()) })
	return &sigConn{conn: c, enc: json.NewEncoder(c), dec: json.NewDecoder(c)}, func() {
		stop()
		_ = c.Close()
	}
}

func (s *sigConn) send(m sigMsg) error { return s.enc.Encode(m) }

func (s *sigConn) recv() (sigMsg, error) {
	var m sigMsg
	err := s.dec.Decode(&m)
	return m, err
}

func computeMAC(password string, nonce []byte) []byte {
	h := hmac.New(sha256.New, []byte(password))
	h.Write(nonce)
	return h.Sum(nil)
}

// Host listens for controller connections.
type Host struct {
	ln       *net.TCPListener
	password string
}

// Listen starts listening for signaling connections on addr (e.g. ":21118").
func Listen(addr, password string) (*Host, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	return &Host{ln: ln.(*net.TCPListener), password: password}, nil
}

// Addr returns the listening address.
func (h *Host) Addr() net.Addr { return h.ln.Addr() }

// Close stops listening. Established peers are not affected.
func (h *Host) Close() error { return h.ln.Close() }

// Accept waits for one controller, verifies its password and negotiates a
// WebRTC session. It returns once the peer's data channels are open. A client
// that fails the challenge gets ErrBadPassword; the host keeps listening and
// Accept may be called again.
func (h *Host) Accept(ctx context.Context) (*Peer, error) {
	stop := context.AfterFunc(ctx, func() { _ = h.ln.SetDeadline(time.Now()) })
	c, err := h.ln.Accept()
	stop()
	_ = h.ln.SetDeadline(time.Time{})
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, err
	}
	sc, done := newSigConn(ctx, c)
	defer done()

	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	if err := sc.send(sigMsg{Nonce: base64.StdEncoding.EncodeToString(nonce)}); err != nil {
		return nil, err
	}
	m, err := sc.recv()
	if err != nil {
		return nil, fmt.Errorf("session: read challenge response: %w", err)
	}
	mac, err := base64.StdEncoding.DecodeString(m.MAC)
	if err != nil || subtle.ConstantTimeCompare(mac, computeMAC(h.password, nonce)) != 1 {
		f := false
		_ = sc.send(sigMsg{OK: &f, Error: "bad password"})
		return nil, ErrBadPassword
	}
	t := true
	if err := sc.send(sigMsg{OK: &t}); err != nil {
		return nil, err
	}

	m, err = sc.recv()
	if err != nil {
		return nil, fmt.Errorf("session: read offer: %w", err)
	}
	if m.SDP == nil || m.SDP.Type != webrtc.SDPTypeOffer {
		return nil, errors.New("session: expected SDP offer")
	}
	p, err := newPeer(true, isLoopback(c.RemoteAddr()))
	if err != nil {
		return nil, err
	}
	answer, err := p.answer(*m.SDP)
	if err != nil {
		p.Close()
		return nil, err
	}
	if err := sc.send(sigMsg{SDP: answer}); err != nil {
		p.Close()
		return nil, err
	}
	done()
	if err := p.waitReady(ctx); err != nil {
		p.Close()
		return nil, err
	}
	return p, nil
}

// Dial connects to a host, answers its password challenge and negotiates a
// WebRTC session as the controller. It returns once the data channels are
// open. A rejected password yields ErrBadPassword.
func Dial(ctx context.Context, addr, password string) (*Peer, error) {
	var d net.Dialer
	c, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	sc, done := newSigConn(ctx, c)
	defer done()

	m, err := sc.recv()
	if err != nil {
		return nil, fmt.Errorf("session: read nonce: %w", err)
	}
	nonce, err := base64.StdEncoding.DecodeString(m.Nonce)
	if err != nil || len(nonce) == 0 {
		return nil, errors.New("session: invalid nonce")
	}
	mac := base64.StdEncoding.EncodeToString(computeMAC(password, nonce))
	if err := sc.send(sigMsg{MAC: mac}); err != nil {
		return nil, err
	}
	m, err = sc.recv()
	if err != nil {
		return nil, fmt.Errorf("session: read auth result: %w", err)
	}
	if m.OK == nil || !*m.OK {
		if m.Error == "bad password" {
			return nil, ErrBadPassword
		}
		return nil, fmt.Errorf("session: rejected: %s", m.Error)
	}

	p, err := newPeer(false, isLoopback(c.RemoteAddr()))
	if err != nil {
		return nil, err
	}
	offer, err := p.offer()
	if err != nil {
		p.Close()
		return nil, err
	}
	if err := sc.send(sigMsg{SDP: offer}); err != nil {
		p.Close()
		return nil, err
	}
	m, err = sc.recv()
	if err != nil {
		p.Close()
		return nil, fmt.Errorf("session: read answer: %w", err)
	}
	if m.SDP == nil || m.SDP.Type != webrtc.SDPTypeAnswer {
		p.Close()
		return nil, errors.New("session: expected SDP answer")
	}
	if err := p.pc.SetRemoteDescription(*m.SDP); err != nil {
		p.Close()
		return nil, err
	}
	done()
	if err := p.waitReady(ctx); err != nil {
		p.Close()
		return nil, err
	}
	return p, nil
}

func isLoopback(a net.Addr) bool {
	if t, ok := a.(*net.TCPAddr); ok {
		return t.IP.IsLoopback()
	}
	return false
}

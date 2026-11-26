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
	"io"
	"net"
	"sync"
	"syscall"
	"time"

	"github.com/pion/webrtc/v4"

	"github.com/tangoui/kkdesk/internal/pairing"
)

// ErrBadPassword is returned by Dial when the host rejects the password, and
// by Host.Accept when a client fails the challenge. The host keeps listening.
var ErrBadPassword = errors.New("session: bad password")

// ErrProbe is returned by Host.Accept when the client closed the connection
// without answering the challenge: a port probe (the 我的设备 online
// check) or an aborted dial. The host keeps listening.
var ErrProbe = errors.New("session: connection closed before authentication")

// signalTimeout bounds the whole TCP signaling exchange when the caller's
// context has no earlier deadline.
const signalTimeout = 30 * time.Second

// sigMsg is one JSON line of the signaling protocol:
//
//	host → client  {"nonce": base64}
//	client → host  {"mac": base64(HMAC-SHA256(password, nonce))}
//	host → client  {"ok": true} | {"ok": false, "error": "bad password"}
//	client → host  {"sdp": offer, "mode": "desktop"|"files"}
//	host → client  {"sdp": answer}
type sigMsg struct {
	Mobile *pairing.Message           `json:"mobile,omitempty"`
	Nonce  string                     `json:"nonce,omitempty"`
	MAC    string                     `json:"mac,omitempty"`
	OK     *bool                      `json:"ok,omitempty"`
	Error  string                     `json:"error,omitempty"`
	SDP    *webrtc.SessionDescription `json:"sdp,omitempty"`
	Mode   Mode                       `json:"mode,omitempty"` // with the offer; "" = desktop
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
	ln *net.TCPListener

	mu        sync.Mutex
	passwords []string // any of them passes the challenge; never ""
	mobile    func(context.Context, *MobileLAN, func())
}

// Listen starts listening for signaling connections on addr (e.g. ":21118").
// A client passes the challenge with any of passwords (the temporary and
// the fixed one); empty passwords are ignored.
func Listen(addr string, passwords ...string) (*Host, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	h := &Host{ln: ln.(*net.TCPListener)}
	h.SetPasswords(passwords...)
	return h, nil
}

// SetPasswords replaces the accepted passwords; the next challenge uses
// them, so a removed password stops working at once. Empty passwords are
// ignored. Established peers are not affected.
func (h *Host) SetPasswords(passwords ...string) {
	var pws []string
	for _, pw := range passwords {
		if pw != "" {
			pws = append(pws, pw)
		}
	}
	h.mu.Lock()
	h.passwords = pws
	h.mu.Unlock()
}

// verify returns the password mac answers nonce with, or ok false. Every
// password is checked, so the time does not tell which one matched.
func (h *Host) verify(mac, nonce []byte) (matched string, ok bool) {
	h.mu.Lock()
	pws := h.passwords
	h.mu.Unlock()
	for _, pw := range pws {
		if subtle.ConstantTimeCompare(mac, computeMAC(pw, nonce)) == 1 {
			matched, ok = pw, true
		}
	}
	return matched, ok
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
	if err := ctx.Err(); err != nil {
		return nil, err
	}
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
	handedOff := false
	defer func() {
		if !handedOff {
			done()
		}
	}()

	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	if err := sc.send(sigMsg{Nonce: base64.StdEncoding.EncodeToString(nonce)}); err != nil {
		return nil, err
	}
	m, err := sc.recv()
	if err != nil {
		if closedEarly(err) {
			return nil, ErrProbe
		}
		return nil, fmt.Errorf("session: read challenge response: %w", err)
	}
	if m.Mobile != nil && h.mobile != nil {
		deadline := time.Now().Add(2 * time.Minute)
		if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
			deadline = d
		}
		_ = sc.conn.SetDeadline(deadline)
		handedOff = true
		h.mobile(ctx, &MobileLAN{sc: sc, first: m.Mobile, close: done}, done)
		return nil, ErrProbe
	}
	mac, err := base64.StdEncoding.DecodeString(m.MAC)
	var authPW string
	var authOK bool
	if err == nil {
		authPW, authOK = h.verify(mac, nonce)
	}
	if !authOK {
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
	p, err := newPeer(true, isLoopback(c.RemoteAddr()), m.Mode)
	if err != nil {
		return nil, err
	}
	p.remote = c.RemoteAddr().String()
	p.SetAuthPassword(authPW)
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
	return DialWithOptions(ctx, addr, password, DialOptions{})
}

// DialWithOptions is Dial with a connection mode. In ModeFiles no video is
// negotiated and the host reads Peer.Mode to skip capture.
func DialWithOptions(ctx context.Context, addr, password string, opts DialOptions) (*Peer, error) {
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

	p, err := newPeer(false, isLoopback(c.RemoteAddr()), opts.Mode)
	if err != nil {
		return nil, err
	}
	p.remote = c.RemoteAddr().String()
	offer, err := p.offer()
	if err != nil {
		p.Close()
		return nil, err
	}
	if err := sc.send(sigMsg{SDP: offer, Mode: p.mode}); err != nil {
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

// closedEarly reports a read error caused by the client closing (EOF or a
// reset), as opposed to a timeout or garbage.
func closedEarly(err error) bool {
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, syscall.ECONNRESET) || errors.Is(err, net.ErrClosed) ||
		closedEarlyOS(err)
}

func isLoopback(a net.Addr) bool {
	if t, ok := a.(*net.TCPAddr); ok {
		return t.IP.IsLoopback()
	}
	return false
}

package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"syscall"
	"time"

	"github.com/pion/webrtc/v4"

	"github.com/tangoui/kkdesk/internal/pairing"
)

// ErrProbe marks a port probe or a mobile exchange handed off by Accept.
var ErrProbe = errors.New("session: connection closed before authentication")

// signalTimeout bounds the whole TCP signaling exchange when the caller's
// context has no earlier deadline.
const signalTimeout = 30 * time.Second

const lanVersion = 2

// LAN signaling starts with the version. The controller replies with either
// a mobile envelope or a WebRTC offer, and receives a signed answer.
type sigMsg struct {
	Mobile  *pairing.Message           `json:"mobile,omitempty"`
	Version int                        `json:"version,omitempty"`
	SDP     *webrtc.SessionDescription `json:"sdp,omitempty"`
	Mode    Mode                       `json:"mode,omitempty"`
	HostKey string                     `json:"hostKey,omitempty"`
	HostSig string                     `json:"hostSig,omitempty"`
}

// maxSignalMsg bounds one signaling message (an SDP with its candidates is
// a few KB): a peer cannot make the other side buffer an endless one.
const maxSignalMsg = 256 << 10

var errSignalTooLarge = errors.New("session: signaling message too large")

// msgLimit reads at most left bytes; recv resets it for every message.
type msgLimit struct {
	r    io.Reader
	left int
}

func (l *msgLimit) Read(p []byte) (int, error) {
	if l.left <= 0 {
		return 0, errSignalTooLarge
	}
	if len(p) > l.left {
		p = p[:l.left]
	}
	n, err := l.r.Read(p)
	l.left -= n
	return n, err
}

type sigConn struct {
	conn  net.Conn
	enc   *json.Encoder
	dec   *json.Decoder
	limit *msgLimit
	// deadline is the whole exchange's deadline (set on conn).
	deadline time.Time
}

func newSigConn(ctx context.Context, c net.Conn) (*sigConn, func()) {
	deadline := time.Now().Add(signalTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = c.SetDeadline(deadline)
	stop := context.AfterFunc(ctx, func() { _ = c.SetDeadline(time.Now()) })
	limit := &msgLimit{r: c, left: maxSignalMsg}
	return &sigConn{conn: c, enc: json.NewEncoder(c), dec: json.NewDecoder(limit), limit: limit, deadline: deadline}, func() {
		stop()
		_ = c.Close()
	}
}

func (s *sigConn) send(m sigMsg) error { return s.enc.Encode(m) }

func (s *sigConn) recv() (sigMsg, error) {
	s.limit.left = maxSignalMsg
	var m sigMsg
	err := s.dec.Decode(&m)
	return m, err
}

// Host listens for controller connections.
type Host struct {
	ln       *net.TCPListener
	identity *pairing.Store
	verify   SourcedVerifier
	mobile   func(context.Context, *MobileLAN, func())
}

// SourcedVerifier checks a password proof from source ("lan:<ip>" for LAN
// connections), so failures can be limited per source.
type SourcedVerifier func(source string, tx PasswordTranscript, proof []byte) (matched string, remaining time.Duration)

// Listen starts listening for v2 signaling connections on addr.
func Listen(addr string, identity *pairing.Store, verify SourcedVerifier) (*Host, error) {
	if identity == nil || verify == nil {
		return nil, errors.New("session: listener requires an identity and password verifier")
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	return &Host{ln: ln.(*net.TCPListener), identity: identity, verify: verify}, nil
}

// Addr returns the listening address.
func (h *Host) Addr() net.Addr { return h.ln.Addr() }

// Close stops listening. Established peers are not affected.
func (h *Host) Close() error { return h.ln.Close() }

// Accept waits for one controller and returns after it proves its password
// over WebRTC. A failed connection leaves the listener open.
func (h *Host) Accept(ctx context.Context) (*Peer, error) {
	c, err := h.AcceptConn(ctx)
	if err != nil {
		return nil, err
	}
	return h.Handshake(ctx, c)
}

// AcceptConn waits for the next TCP connection; Handshake runs its
// signaling. Callers that run handshakes concurrently can limit them per
// address in between.
func (h *Host) AcceptConn(ctx context.Context) (net.Conn, error) {
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
	return c, nil
}

// firstMessageWait bounds how long a new connection may stay silent: the
// controller sends its offer after gathering ICE candidates (gatherWait here,
// 8 s on the phone), with room for slow machines.
const firstMessageWait = 20 * time.Second

// Handshake authenticates a connection from AcceptConn and returns the
// peer; it closes c on failure. A phone's pairing exchange is handed to the
// mobile handler, which Handshake waits for, and reported as ErrProbe.
func (h *Host) Handshake(ctx context.Context, c net.Conn) (*Peer, error) {
	sc, done := newSigConn(ctx, c)
	handedOff := false
	defer func() {
		if !handedOff {
			done()
		}
	}()

	if err := sc.send(sigMsg{Version: lanVersion}); err != nil {
		return nil, err
	}
	if first := time.Now().Add(firstMessageWait); first.Before(sc.deadline) {
		_ = c.SetReadDeadline(first)
	}
	m, err := sc.recv()
	_ = c.SetReadDeadline(sc.deadline)
	if ctx.Err() != nil { // restoring the deadline may have undone the cancel
		return nil, ctx.Err()
	}
	if err != nil {
		if closedEarly(err) {
			return nil, ErrProbe
		}
		return nil, fmt.Errorf("session: read offer: %w", err)
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
	if m.SDP == nil || m.SDP.Type != webrtc.SDPTypeOffer {
		return nil, ErrHostSignature
	}
	p, err := newPeer(true, isLoopback(c.RemoteAddr()), m.Mode)
	if err != nil {
		return nil, err
	}
	p.remote = c.RemoteAddr().String()
	p.requirePassword()
	answer, err := p.answer(*m.SDP)
	if err != nil {
		p.Close()
		return nil, err
	}
	if err := sc.send(sigMsg{SDP: answer, HostKey: h.identity.PublicKey(), HostSig: h.identity.SignHostAnswer(m.SDP.SDP, answer.SDP)}); err != nil {
		p.Close()
		return nil, err
	}
	done()
	source := SourceKey(hostOnly(c.RemoteAddr()))
	check := func(tx PasswordTranscript, proof []byte) (string, time.Duration) { return h.verify(source, tx, proof) }
	if err := p.AcceptPassword(ctx, NewPasswordTranscript(m.SDP.SDP, answer.SDP), check); err != nil {
		p.Close()
		return nil, err
	}
	return p, nil
}

// Dial connects to a host and authenticates over WebRTC as the controller.
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
		return nil, fmt.Errorf("session: read greeting: %w", err)
	}
	if m.Version != lanVersion {
		return nil, ErrHostSignature
	}

	p, err := newPeer(false, isLoopback(c.RemoteAddr()), opts.Mode)
	if err != nil {
		return nil, err
	}
	p.remote = c.RemoteAddr().String()
	p.requirePassword()
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
	if m.SDP == nil || m.SDP.Type != webrtc.SDPTypeAnswer ||
		!pairing.VerifyHostAnswer(m.HostKey, m.HostSig, offer.SDP, m.SDP.SDP) {
		p.Close()
		return nil, ErrHostSignature
	}
	if opts.ExpectedHostKey != "" && opts.ExpectedHostKey != m.HostKey {
		p.Close()
		return nil, ErrHostKeyChanged
	}
	p.hostKey = m.HostKey
	if err := p.pc.SetRemoteDescription(*m.SDP); err != nil {
		p.Close()
		return nil, err
	}
	done()
	if err := p.provePassword(ctx, NewPasswordTranscript(offer.SDP, m.SDP.SDP), password); err != nil {
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

// hostOnly is the IP of a TCP address (the whole string if it has no port).
func hostOnly(a net.Addr) string {
	host, _, err := net.SplitHostPort(a.String())
	if err != nil {
		return a.String()
	}
	return host
}

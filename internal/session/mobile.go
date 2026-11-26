package session

import (
	"context"
	"io"
	"net"

	"github.com/pion/webrtc/v4"

	"github.com/tangoui/kkdesk/internal/pairing"
	"github.com/tangoui/kkdesk/internal/rendezvous"
)

// SetMobileHandler installs the owner of LAN mobile exchanges before Accept
// starts. The handler must call release after it finishes with the connection.
func (h *Host) SetMobileHandler(handler func(context.Context, *MobileLAN, func())) {
	h.mobile = handler
}

type MobileLAN struct {
	sc    *sigConn
	first *pairing.Message
	close func()
}

func OpenMobileLAN(ctx context.Context, address string) (*MobileLAN, error) {
	var dial net.Dialer
	conn, err := dial.DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, err
	}
	sc, close := newSigConn(ctx, conn)
	if msg, err := sc.recv(); err != nil || msg.Version != lanVersion {
		close()
		return nil, ErrHostSignature
	}
	return &MobileLAN{sc: sc, close: close}, nil
}

func (l *MobileLAN) Send(_ context.Context, msg pairing.Message) error {
	return l.sc.send(sigMsg{Mobile: &msg})
}

func (l *MobileLAN) Receive(_ context.Context) (pairing.Message, error) {
	if l.first != nil {
		msg := *l.first
		l.first = nil
		return msg, nil
	}
	msg, err := l.sc.recv()
	if err != nil {
		return pairing.Message{}, err
	}
	if msg.Mobile == nil {
		return pairing.Message{}, io.ErrUnexpectedEOF
	}
	return *msg.Mobile, nil
}

func (l *MobileLAN) Close() { l.close() }

func AnswerMobileLAN(l *MobileLAN, mode Mode, offer webrtc.SessionDescription) (*Peer, *webrtc.SessionDescription, error) {
	p, err := newPeer(true, isLoopback(l.sc.conn.RemoteAddr()), mode)
	if err != nil {
		return nil, nil, err
	}
	p.remote = l.sc.conn.RemoteAddr().String()
	return answerMobile(p, offer)
}

func AnswerMobileRemote(cfg rendezvous.Config, e *rendezvous.HostMobileExchange, mode Mode, offer webrtc.SessionDescription) (*Peer, *webrtc.SessionDescription, error) {
	p, err := newRemotePeer(cfg, true, mode, e.ICE, e.RelayOnly)
	if err != nil {
		return nil, nil, err
	}
	p.remote = "跨网连接"
	return answerMobile(p, offer)
}

func answerMobile(p *Peer, offer webrtc.SessionDescription) (*Peer, *webrtc.SessionDescription, error) {
	answer, err := p.answer(offer)
	if err != nil {
		p.Close()
		return nil, nil, err
	}
	return p, answer, nil
}

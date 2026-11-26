package session

import (
	"context"

	"github.com/pion/webrtc/v4"

	"github.com/tangoui/kkdesk/internal/pairing"
	"github.com/tangoui/kkdesk/internal/rendezvous"
)

// DialRemote verifies the signed answer against code before proving the
// password over WebRTC.
func DialRemote(ctx context.Context, cfg rendezvous.Config, code string, mode Mode, password string) (*Peer, error) {
	return DialRemoteAs(ctx, cfg, code, mode, password, nil)
}

// DialRemoteAs is DialRemote proving identity to the host (see
// DialOptions.Identity); nil sends none.
func DialRemoteAs(ctx context.Context, cfg rendezvous.Config, code string, mode Mode, password string, identity *pairing.Store) (*Peer, error) {
	guest, err := rendezvous.Join(ctx, cfg, code)
	if err != nil {
		return nil, err
	}
	defer guest.Close()
	p, err := newRemotePeer(cfg, false, mode, guest.ICE, guest.RelayOnly)
	if err != nil {
		return nil, err
	}
	p.requirePassword()
	p.identity = identity
	offer, err := p.offer()
	var answer webrtc.SessionDescription
	if err == nil {
		answer, err = guest.Exchange(ctx, string(p.mode), offer)
		if err == nil {
			signed := answer.SDP
			answer.SDP, err = verifyRemoteAnswer(offer.SDP, signed, code)
			if err == nil {
				p.hostKey = remoteAnswerKey(signed)
			}
		}
		if err == nil {
			err = p.pc.SetRemoteDescription(answer)
		}
	}
	if err == nil {
		err = p.provePassword(ctx, NewPasswordTranscript(offer.SDP, answer.SDP), password)
	}
	if err != nil {
		p.Close()
		return nil, err
	}
	p.remote = code
	return p, nil
}

func AnswerRemote(cfg rendezvous.Config, req rendezvous.Offer, identity *pairing.Store) (*Peer, *webrtc.SessionDescription, PasswordTranscript, error) {
	if identity == nil {
		return nil, nil, PasswordTranscript{}, ErrHostSignature
	}
	p, err := newRemotePeer(cfg, true, Mode(req.Mode), req.ICE, req.RelayOnly)
	if err != nil {
		return nil, nil, PasswordTranscript{}, err
	}
	p.requirePassword()
	answer, err := p.answer(req.SDP)
	if err != nil {
		p.Close()
		return nil, nil, PasswordTranscript{}, err
	}
	p.remote = "跨网连接"
	transcript := NewPasswordTranscript(req.SDP.SDP, answer.SDP)
	answer.SDP = signRemoteAnswer(identity, req.SDP.SDP, answer.SDP)
	return p, answer, transcript, nil
}

func (p *Peer) WaitReady(ctx context.Context) error { return p.waitReady(ctx) }

func newRemotePeer(cfg rendezvous.Config, isHost bool, mode Mode, servers []webrtc.ICEServer, relayOnly bool) (*Peer, error) {
	policy := webrtc.ICETransportPolicyAll
	if relayOnly {
		policy = webrtc.ICETransportPolicyRelay
	}
	dialer, err := cfg.TURNProxy()
	if err != nil {
		return nil, err
	}
	return newConfiguredPeer(isHost, false, mode, webrtc.Configuration{ICEServers: servers, ICETransportPolicy: policy}, dialer)
}

// Relayed reports whether the nominated ICE pair uses a relay candidate.
func (p *Peer) Relayed() bool {
	pair, err := p.pc.SCTP().Transport().ICETransport().GetSelectedCandidatePair()
	return err == nil && pair != nil &&
		(pair.Local.Typ == webrtc.ICECandidateTypeRelay || pair.Remote.Typ == webrtc.ICECandidateTypeRelay)
}

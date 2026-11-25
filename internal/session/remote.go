package session

import (
	"context"

	"github.com/pion/webrtc/v4"

	"github.com/tangoui/kkdesk/internal/rendezvous"
)

func DialRemote(ctx context.Context, cfg rendezvous.Config, code string, mode Mode) (*Peer, error) {
	guest, err := rendezvous.Join(ctx, cfg, code)
	if err != nil {
		return nil, err
	}
	defer guest.Close()
	p, err := newConfiguredPeer(false, false, mode, remoteICE(guest.ICE, guest.RelayOnly))
	if err != nil {
		return nil, err
	}
	offer, err := p.offer()
	if err == nil {
		var answer webrtc.SessionDescription
		answer, err = guest.Exchange(ctx, string(p.mode), offer)
		if err == nil {
			err = p.pc.SetRemoteDescription(answer)
		}
	}
	if err == nil {
		err = p.waitReady(ctx)
	}
	if err != nil {
		p.Close()
		return nil, err
	}
	p.remote = code
	return p, nil
}

func AnswerRemote(req rendezvous.Offer) (*Peer, *webrtc.SessionDescription, error) {
	p, err := newConfiguredPeer(true, false, Mode(req.Mode), remoteICE(req.ICE, req.RelayOnly))
	if err != nil {
		return nil, nil, err
	}
	answer, err := p.answer(req.SDP)
	if err != nil {
		p.Close()
		return nil, nil, err
	}
	p.remote = "跨网连接"
	return p, answer, nil
}

func (p *Peer) WaitReady(ctx context.Context) error { return p.waitReady(ctx) }

func remoteICE(servers []webrtc.ICEServer, relayOnly bool) webrtc.Configuration {
	policy := webrtc.ICETransportPolicyAll
	if relayOnly {
		policy = webrtc.ICETransportPolicyRelay
	}
	return webrtc.Configuration{ICEServers: servers, ICETransportPolicy: policy}
}

// Relayed reports whether the nominated ICE pair uses a relay candidate.
func (p *Peer) Relayed() bool {
	pair, err := p.pc.SCTP().Transport().ICETransport().GetSelectedCandidatePair()
	return err == nil && pair != nil &&
		(pair.Local.Typ == webrtc.ICECandidateTypeRelay || pair.Remote.Typ == webrtc.ICECandidateTypeRelay)
}

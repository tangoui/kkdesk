package session

import (
	"context"
	"net"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/tangoui/kkdesk/internal/rendezvous"
)

func TestRemoteVideoAndInputThroughTURN(t *testing.T) {
	const token = "test-access-token-of-sufficient-length"
	const secret = "test-turn-secret-of-sufficient-length"
	relay, err := rendezvous.StartRelay(rendezvous.RelayConfig{
		UDPAddr: "127.0.0.1:0", BindIP: "127.0.0.1", PublicIP: net.IPv4(127, 0, 0, 1),
		MinPort: 49360, MaxPort: 49459, Secret: secret,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()
	broker, err := rendezvous.NewServer(token, secret, []string{"turn:" + relay.UDPAddr.String()})
	if err != nil {
		t.Fatal(err)
	}
	broker.RelayOnly = true
	server := httptest.NewServer(broker)
	defer server.Close()
	cfg := rendezvous.Config{URL: server.URL, Token: token}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	code := rendezvous.NewCode()
	host, err := rendezvous.Register(ctx, cfg, code)
	if err != nil {
		t.Fatal(err)
	}
	defer host.CloseNow()
	type result struct {
		peer *Peer
		err  error
	}
	answered := make(chan result)
	go func() {
		offer, err := rendezvous.ReadOffer(ctx, host)
		var p *Peer
		if err == nil {
			p, err = acceptRemoteForTest(ctx, host, offer)
		}
		select {
		case answered <- result{p, err}:
		case <-ctx.Done():
			if p != nil {
				p.Close()
			}
		}
	}()
	controller, err := DialRemote(ctx, cfg, code, ModeDesktop)
	if err != nil {
		t.Fatal(err)
	}
	defer controller.Close()
	select {
	case res := <-answered:
		if res.err != nil {
			t.Fatal(res.err)
		}
		defer res.peer.Close()
		if !controller.Relayed() {
			t.Fatal("connection did not use TURN")
		}
		testVideoAndInput(t, res.peer, controller)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

func acceptRemoteForTest(ctx context.Context, host *websocket.Conn, offer rendezvous.Offer) (*Peer, error) {
	p, answer, err := AnswerRemote(offer)
	if err != nil {
		return nil, err
	}
	if err = rendezvous.Reply(ctx, host, offer.ID, answer, ""); err == nil {
		err = p.WaitReady(ctx)
	}
	if err != nil {
		p.Close()
		return nil, err
	}
	return p, nil
}

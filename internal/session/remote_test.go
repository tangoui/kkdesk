package session

import (
	"context"
	"crypto/tls"
	"net"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/tangoui/kkdesk/internal/rendezvous"
)

func TestRemoteVideoAndInputThroughTURN(t *testing.T) {
	t.Run("UDP", func(t *testing.T) { testRemoteVideoAndInputThroughTURN(t, false) })
	t.Run("pinned TLS", func(t *testing.T) { testRemoteVideoAndInputThroughTURN(t, true) })
}

func testRemoteVideoAndInputThroughTURN(t *testing.T, pinned bool) {
	t.Helper()
	const token = "test-access-token-of-sufficient-length"
	const secret = "test-turn-secret-of-sufficient-length"
	cfg := rendezvous.Config{Token: token}
	relayConfig := rendezvous.RelayConfig{
		UDPAddr: "127.0.0.1:0", BindIP: "127.0.0.1", PublicIP: net.IPv4(127, 0, 0, 1),
		MinPort: 49360, MaxPort: 49459, Secret: secret,
	}
	var serverTLS *tls.Config
	if pinned {
		var err error
		serverTLS, cfg.PublicKey, err = rendezvous.LoadOrCreateIdentity(filepath.Join(t.TempDir(), "identity.pem"))
		if err != nil {
			t.Fatal(err)
		}
		relayConfig.TLSAddr, relayConfig.TLS = "127.0.0.1:0", serverTLS.Clone()
	}
	relay, err := rendezvous.StartRelay(relayConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()
	relayURL := "turn:" + relay.UDPAddr.String()
	if pinned {
		relayURL = "turns:" + relay.TLSAddr.String() + "?transport=tcp"
	}
	broker, err := rendezvous.NewServer(token, secret, []string{relayURL})
	if err != nil {
		t.Fatal(err)
	}
	broker.RelayOnly = true
	server := httptest.NewUnstartedServer(broker)
	if pinned {
		server.TLS = serverTLS
		server.StartTLS()
	} else {
		server.Start()
	}
	defer server.Close()
	cfg.URL = server.URL
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
			p, err = acceptRemoteForTest(ctx, cfg, host, offer)
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

func acceptRemoteForTest(ctx context.Context, cfg rendezvous.Config, host *websocket.Conn, offer rendezvous.Offer) (*Peer, error) {
	p, answer, err := AnswerRemote(cfg, offer)
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

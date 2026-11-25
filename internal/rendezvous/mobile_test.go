package rendezvous

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tangoui/kkdesk/internal/pairing"
)

func TestMobileRelayAndPresence(t *testing.T) {
	broker, err := NewServer(strings.Repeat("a", 32), strings.Repeat("b", 32), []string{"turn:127.0.0.1:3478"})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(broker)
	defer server.Close()
	cfg := Config{URL: server.URL, Token: strings.Repeat("a", 32)}
	identity, err := pairing.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if online, err := MobileOnline(ctx, cfg, identity.ID()); err != nil || online {
		t.Fatalf("before register: online=%v, %v", online, err)
	}
	host, err := RegisterMobile(ctx, cfg, identity)
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close()
	go host.Serve(ctx, func(ctx context.Context, exchange MobileExchange) {
		request, err := exchange.Receive(ctx)
		if err == nil {
			_ = exchange.Send(ctx, pairing.Message{Type: "result", Status: request.Purpose})
		}
	})
	if online, err := MobileOnline(ctx, cfg, identity.ID()); err != nil || !online {
		t.Fatalf("after register: online=%v, %v", online, err)
	}
	guest, err := OpenMobile(ctx, cfg, identity.ID(), false)
	if err != nil {
		t.Fatal(err)
	}
	defer guest.Close()
	if err := guest.Send(ctx, pairing.Message{Type: "hello", Purpose: "pair"}); err != nil {
		t.Fatal(err)
	}
	if result, err := guest.Receive(ctx); err != nil || result.Type != "result" || result.Status != "pair" {
		t.Fatalf("relay response: %+v, %v", result, err)
	}
	cancelled, err := OpenMobile(ctx, cfg, identity.ID(), false)
	if err != nil {
		t.Fatal(err)
	}
	cancelled.Close()
	other, err := OpenMobile(ctx, cfg, identity.ID(), false)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if err := other.Send(ctx, pairing.Message{Type: "hello", Purpose: "status"}); err != nil {
		t.Fatal(err)
	}
	if result, err := other.Receive(ctx); err != nil || result.Status != "status" {
		t.Fatalf("relay disconnected after another guest closed: %+v, %v", result, err)
	}
}

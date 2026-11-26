package rendezvous

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// A phone's QR code carries a credential for one computer: it reaches that
// computer's phone channel and nothing else on the service.
func TestMobileTokenIsScopedToOneComputer(t *testing.T) {
	const token = "private-test-access-token-of-sufficient-length"
	broker, err := NewServer(token, strings.Repeat("b", 32), []string{"turn:127.0.0.1:3478"})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(broker)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	full := Config{URL: server.URL, Token: token}
	mine, other := newIdentity(t), newIdentity(t)
	host, err := RegisterMobile(ctx, full, mine)
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close()

	phone := Config{URL: server.URL, Token: MobileToken(token, mine.ID())}
	if strings.Contains(phone.Token, token) {
		t.Fatal("the phone credential contains the service token")
	}
	if online, err := MobileOnline(ctx, phone, mine.ID()); err != nil || !online {
		t.Fatalf("scoped status of its computer = %v, %v; want online", online, err)
	}
	if _, err := MobileOnline(ctx, phone, other.ID()); err == nil {
		t.Fatal("the scoped credential queried another computer")
	}
	if c, err := Register(ctx, phone, NewCode()); err == nil {
		c.CloseNow()
		t.Fatal("the scoped credential registered a device code")
	}
	if _, err := Join(ctx, phone, mine.DeviceCode()); err == nil {
		t.Fatal("the scoped credential joined a desktop")
	}
	if _, err := RegisterMobile(ctx, phone, other); err == nil {
		t.Fatal("the scoped credential registered a computer's phone channel")
	}
	forged := Config{URL: server.URL, Token: MobileToken("some-other-service-token-value-here", mine.ID())}
	if _, err := MobileOnline(ctx, forged, mine.ID()); err == nil {
		t.Fatal("a credential derived from another token was accepted")
	}
}

package rendezvous

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pion/webrtc/v4"
)

func TestServerOptionsAndHealth(t *testing.T) {
	const token = "private-test-access-token-of-sufficient-length"
	server, err := NewServerWithOptions(token, "test-turn-secret-with-sufficient-entropy",
		[]string{"turn:127.0.0.1:3478"}, ServerOptions{
			MaxConnections: 2, CredentialTTL: 2 * time.Hour,
		})
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cfg := Config{URL: httpServer.URL, Token: token}

	code := NewCode()
	host, err := Register(ctx, cfg, code)
	if err != nil {
		t.Fatal(err)
	}
	defer host.CloseNow()
	before := time.Now().Add(2 * time.Hour).Unix()
	guest, err := Join(ctx, cfg, code)
	if err != nil {
		t.Fatal(err)
	}
	defer guest.Close()
	stamp, _, _ := strings.Cut(guest.ICE[0].Username, ":")
	expires, err := strconv.ParseInt(stamp, 10, 64)
	if err != nil || expires < before || expires > time.Now().Add(2*time.Hour).Unix() {
		t.Fatalf("unexpected TURN expiry %q", stamp)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, httpServer.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := httpServer.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when the signaling limit is reached, got %d", resp.StatusCode)
	}

	req, err = http.NewRequestWithContext(ctx, http.MethodGet, httpServer.URL+"/healthz", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err = httpServer.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != http.StatusOK || string(body) != "ok\n" {
		t.Fatalf("health check returned %d %q, %v", resp.StatusCode, body, err)
	}
}

func TestPairingRequiresAccessAndForwardsOnlyMatchedSignaling(t *testing.T) {
	const token = "private-test-access-token-of-sufficient-length"
	server, err := NewServer(token, "test-turn-secret-with-sufficient-entropy", []string{"stun:127.0.0.1:3478", "turn:127.0.0.1:3478"})
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cfg := Config{URL: httpServer.URL, Token: token}
	bad := Config{URL: cfg.URL, Token: "wrong"}
	if conn, err := bad.dial(ctx); err == nil {
		conn.CloseNow()
		t.Fatal("unauthorized client connected")
	}
	code := NewCode()
	host, err := Register(ctx, cfg, code)
	if err != nil {
		t.Fatal(err)
	}
	defer host.CloseNow()
	if duplicate, err := Register(ctx, cfg, code); err == nil {
		duplicate.CloseNow()
		t.Fatal("duplicate registration took over the device")
	}
	if missing, err := Join(ctx, cfg, NewCode()); err == nil {
		missing.Close()
		t.Fatal("joined an offline device")
	}
	guest, err := Join(ctx, cfg, code)
	if err != nil {
		t.Fatal(err)
	}
	defer guest.Close()
	if len(guest.ICE) != 2 || guest.ICE[1].Credential == "" {
		t.Fatalf("missing TURN configuration: %+v", guest.ICE)
	}
	if err := write(ctx, guest.conn, message{Type: "offer", Mode: "files", SDP: &webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: "offer"}}); err != nil {
		t.Fatal(err)
	}
	offer, err := ReadOffer(ctx, host)
	if err != nil || offer.Mode != "files" || offer.SDP.SDP != "offer" || len(offer.ICE) != 2 {
		t.Fatalf("host did not receive the offer: %+v, %v", offer, err)
	}
	answer := &webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: "answer"}
	cancelled, stopReply := context.WithCancel(ctx)
	stopReply()
	if err := Reply(cancelled, host, offer.ID, nil, "late reply"); !errors.Is(err, context.Canceled) {
		t.Fatalf("reply after cancellation returned %v", err)
	}
	if err := Reply(ctx, host, "unmatched", answer, ""); err != nil {
		t.Fatal(err)
	}
	if err := Reply(ctx, host, offer.ID, answer, ""); err != nil {
		t.Fatal(err)
	}
	msg, err := read(ctx, guest.conn)
	if err != nil || msg.Type != "answer" || msg.SDP == nil || msg.SDP.SDP != "answer" {
		t.Fatalf("guest did not receive the answer: %+v, %v", msg, err)
	}
}

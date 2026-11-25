package rendezvous

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestPinnedTLSAuthenticatesBeforeSendingToken(t *testing.T) {
	token := "a-test-token-at-least-thirty-two-characters"
	identity := filepath.Join(t.TempDir(), "identity.pem")
	cert, key, err := LoadOrCreateIdentity(identity)
	if err != nil {
		t.Fatal(err)
	}
	_, persisted, err := LoadOrCreateIdentity(identity)
	if err != nil || persisted != key {
		t.Fatal("server public key changed after reloading the identity")
	}
	var requests atomic.Int32
	broker, err := NewServer(token, "test-turn-secret-with-sufficient-entropy", []string{"turn:127.0.0.1:3478"})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		broker.ServeHTTP(w, r)
	}))
	server.TLS = cert
	server.StartTLS()
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, wrong, err := LoadOrCreateIdentity(filepath.Join(t.TempDir(), "other.pem"))
	if err != nil {
		t.Fatal(err)
	}
	if conn, err := Register(ctx, Config{URL: server.URL, Token: token, PublicKey: wrong}, NewCode()); err == nil {
		conn.CloseNow()
		t.Fatal("accepted an unexpected server key")
	}
	if requests.Load() != 0 {
		t.Fatal("sent an HTTP request before authenticating the server")
	}

	cfg := Config{URL: server.URL, Token: token, PublicKey: key}
	host, err := Register(ctx, cfg, NewCode())
	if err != nil {
		t.Fatal(err)
	}
	host.CloseNow()
	if requests.Load() != 1 {
		t.Fatalf("received %d authenticated requests", requests.Load())
	}
	for _, url := range []string{"http://127.0.0.1:8080", "https://example.com"} {
		cfg.URL = url
		if err := cfg.Validate(); err == nil {
			t.Fatalf("accepted a public key with %s", url)
		}
	}
}

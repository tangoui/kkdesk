package rendezvous

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/tangoui/kkdesk/internal/pairing"
)

func identityTestServer(t *testing.T) (Config, context.Context) {
	t.Helper()
	broker, err := NewServer(strings.Repeat("a", 32), strings.Repeat("b", 32), []string{"turn:127.0.0.1:3478"})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(broker)
	t.Cleanup(server.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return Config{URL: server.URL, Token: strings.Repeat("a", 32)}, ctx
}

func newIdentity(t *testing.T) *pairing.Store {
	t.Helper()
	id, err := pairing.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// closed reports whether the server ended conn (a read fails promptly).
func closed(ctx context.Context, conn *websocket.Conn) bool {
	rctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	_, _, err := conn.Read(rctx)
	return err != nil && rctx.Err() == nil
}

// Someone holding the service token cannot keep a device's code: the
// device's signed registration replaces an unsigned one.
func TestSignedRegistrationEvictsSquatter(t *testing.T) {
	cfg, ctx := identityTestServer(t)
	id := newIdentity(t)
	squatter, err := Register(ctx, cfg, id.DeviceCode())
	if err != nil {
		t.Fatal(err)
	}
	defer squatter.CloseNow()
	host, err := RegisterIdentity(ctx, cfg, id)
	if err != nil {
		t.Fatalf("signed registration behind a squatter: %v", err)
	}
	defer host.CloseNow()
	if !closed(ctx, squatter) {
		t.Fatal("the squatter's registration stayed open")
	}
}

func TestUnsignedCannotDisplaceSigned(t *testing.T) {
	cfg, ctx := identityTestServer(t)
	id := newIdentity(t)
	host, err := RegisterIdentity(ctx, cfg, id)
	if err != nil {
		t.Fatal(err)
	}
	defer host.CloseNow()
	if c, err := Register(ctx, cfg, id.DeviceCode()); err == nil {
		c.CloseNow()
		t.Fatal("an unsigned registration took a signed device's code")
	}
}

// The same device registering again (a reconnect before the server noticed
// the old socket died) replaces its own stale registration.
func TestSignedReregistrationReplacesStale(t *testing.T) {
	cfg, ctx := identityTestServer(t)
	id := newIdentity(t)
	old, err := RegisterIdentity(ctx, cfg, id)
	if err != nil {
		t.Fatal(err)
	}
	defer old.CloseNow()
	fresh, err := RegisterIdentity(ctx, cfg, id)
	if err != nil {
		t.Fatalf("re-registration: %v", err)
	}
	defer fresh.CloseNow()
	if !closed(ctx, old) {
		t.Fatal("the stale registration stayed open")
	}
	// The fresh registration is the one that stays: a join reaches it.
	if _, err := Join(ctx, cfg, id.DeviceCode()); err != nil {
		t.Fatalf("join after re-registration: %v", err)
	}
}

// A key whose device code is not the requested code is refused.
func TestSignedRegistrationRejectsAnotherDevicesCode(t *testing.T) {
	cfg, ctx := identityTestServer(t)
	victim, attacker := newIdentity(t), newIdentity(t)
	if _, err := registerSigned(ctx, cfg, victim.DeviceCode(), attacker); err == nil {
		t.Fatal("a key registered another device's code")
	}
}

// Against a server without signed registration (it answers "registered"
// at once) a new client still registers.
func TestSignedRegistrationWithOldServer(t *testing.T) {
	old := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		if _, err := read(r.Context(), c); err != nil {
			return
		}
		_ = write(r.Context(), c, message{Type: "registered"})
		<-r.Context().Done()
	}))
	defer old.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := RegisterIdentity(ctx, Config{URL: old.URL, Token: "t"}, newIdentity(t))
	if err != nil {
		t.Fatalf("registering with an old server: %v", err)
	}
	c.CloseNow()
}

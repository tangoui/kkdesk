package session

import (
	"context"
	"testing"
	"time"

	"github.com/tangoui/kkdesk/internal/pairing"
)

// A controller with an identity proves it to the host, which sees the key
// while checking the password; without one the host sees none.
func TestViewerIdentityReachesHostVerifier(t *testing.T) {
	hostID, err := pairing.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	viewerID, err := pairing.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	seen := make(chan string, 4)
	host, err := Listen("127.0.0.1:0", hostID, func(source, viewerKey string, tx PasswordTranscript, proof []byte) (string, time.Duration) {
		seen <- viewerKey
		var gate PasswordGate
		return gate.Check(time.Now(), source, tx, proof, testPassword, "")
	})
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	go func() {
		for ctx.Err() == nil {
			if p, err := host.Accept(ctx); err == nil {
				defer p.Close()
			}
		}
	}()
	for _, tc := range []struct {
		id   *pairing.Store
		want string
	}{{viewerID, viewerID.ViewerPublicKey(hostID.PublicKey())}, {nil, ""}} {
		p, err := DialWithOptions(ctx, host.Addr().String(), testPassword, DialOptions{Mode: ModeFiles, Identity: tc.id})
		if err != nil {
			t.Fatal(err)
		}
		p.Close()
		if got := <-seen; got != tc.want {
			t.Errorf("host saw viewer key %q, want %q", got, tc.want)
		}
	}
}

func TestViewerClaimBoundToTranscript(t *testing.T) {
	viewerID, err := pairing.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tx := NewPasswordTranscript("offer", "answer")
	const hostKey = "host-key"
	claim := viewerClaim(viewerID, hostKey, tx)
	if got := verifyViewerClaim(claim, tx); got != viewerID.ViewerPublicKey(hostKey) {
		t.Fatalf("valid claim = %q", got)
	}
	if got := verifyViewerClaim(claim, NewPasswordTranscript("offer", "other answer")); got != "" {
		t.Fatalf("a claim for another connection was accepted: %q", got)
	}
	if got := verifyViewerClaim(claim[:len(claim)-1], tx); got != "" {
		t.Fatalf("a short claim was accepted: %q", got)
	}
}

// A controller can learn a host's key before sending any password proof, to
// have the user check the device code first.
func TestProbeHostKey(t *testing.T) {
	hostID, err := pairing.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	host, err := Listen("127.0.0.1:0", hostID, func(string, string, PasswordTranscript, []byte) (string, time.Duration) {
		t.Error("a probe reached password verification")
		return "", 0
	})
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	accepted := make(chan error, 1)
	go func() { _, err := host.Accept(ctx); accepted <- err }()
	key, err := ProbeHostKey(ctx, host.Addr().String())
	if err != nil || key != hostID.PublicKey() {
		t.Fatalf("ProbeHostKey = %q, %v; want the host's key", key, err)
	}
	if err := <-accepted; err != ErrProbe {
		t.Fatalf("host saw the probe as %v, want ErrProbe", err)
	}
}

// The key a controller proves is derived per host: a host cannot learn the
// controller's own key (and from it its device code) nor link it across
// hosts.
func TestViewerKeyIsPerHost(t *testing.T) {
	viewerID, err := pairing.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a, b := viewerID.ViewerPublicKey("host-a"), viewerID.ViewerPublicKey("host-b")
	if a == viewerID.PublicKey() || b == viewerID.PublicKey() {
		t.Fatal("the proved key is the controller's own identity key")
	}
	if a == b {
		t.Fatal("two hosts see the same controller key")
	}
	if a != viewerID.ViewerPublicKey("host-a") {
		t.Fatal("the per-host key is not stable")
	}
}

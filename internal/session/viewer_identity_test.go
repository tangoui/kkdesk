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
	}{{viewerID, viewerID.PublicKey()}, {nil, ""}} {
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
	claim := viewerClaim(viewerID, tx)
	if got := verifyViewerClaim(claim, tx); got != viewerID.PublicKey() {
		t.Fatalf("valid claim = %q", got)
	}
	if got := verifyViewerClaim(claim, NewPasswordTranscript("offer", "other answer")); got != "" {
		t.Fatalf("a claim for another connection was accepted: %q", got)
	}
	if got := verifyViewerClaim(claim[:len(claim)-1], tx); got != "" {
		t.Fatalf("a short claim was accepted: %q", got)
	}
}

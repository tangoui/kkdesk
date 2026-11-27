package session

import (
	"bytes"
	"testing"
	"time"

	"github.com/pion/webrtc/v4"
)

func TestUnlockRequestAdmissionAndValidation(t *testing.T) {
	for _, tc := range []struct {
		name     string
		host     bool
		mode     Mode
		pending  bool
		admitted bool
		closed   bool
		secret   []byte
		text     bool
		want     bool
	}{
		{name: "valid", host: true, admitted: true, secret: []byte("PIN-123"), want: true},
		{name: "pending password", host: true, pending: true, admitted: true, secret: []byte("123")},
		{name: "waiting for confirmation", host: true, secret: []byte("123")},
		{name: "files only", host: true, mode: ModeFiles, admitted: true, secret: []byte("123")},
		{name: "closed", host: true, admitted: true, closed: true, secret: []byte("123")},
		{name: "wrong direction", admitted: true, secret: []byte("123")},
		{name: "invalid UTF-8", host: true, admitted: true, secret: []byte{0xff}},
		{name: "text channel message", host: true, admitted: true, secret: []byte("123"), text: true},
		{name: "empty password", host: true, admitted: true},
		{name: "oversized", host: true, admitted: true, secret: bytes.Repeat([]byte{'a'}, maxUnlockBytes+1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &Peer{isHost: tc.host, mode: tc.mode, done: make(chan struct{})}
			if tc.pending {
				p.requirePassword()
			}
			if tc.closed {
				close(p.done)
			}
			delivered := false
			if tc.admitted {
				p.OnUnlock(func(secret []byte) {
					delivered = true
					if !bytes.Equal(secret, tc.secret) {
						t.Error("unexpected unlock payload")
					}
				})
			}
			frame := append([]byte{metaUnlockRequest}, tc.secret...)
			p.handleMeta(webrtc.DataChannelMessage{Data: frame, IsString: tc.text})
			if delivered != tc.want {
				t.Fatalf("delivered = %v, want %v", delivered, tc.want)
			}
			if !bytes.Equal(frame[1:], make([]byte, len(tc.secret))) {
				t.Error("request buffer was not cleared")
			}
		})
	}
}

func TestLockScreenStateBeforeSubscription(t *testing.T) {
	p := &Peer{}
	for _, state := range []LockScreenState{LockScreenReady, LockScreenComponentUnavailable} {
		p.handleMeta(webrtc.DataChannelMessage{Data: []byte{metaLockScreen, byte(state)}})
	}
	seen := 0
	p.OnLockScreenChange(func() {
		seen++
		if state := p.LockScreen(); state != LockScreenComponentUnavailable {
			t.Fatalf("got state %v", state)
		}
	})
	if seen != 1 {
		t.Fatalf("got %d notifications, want one", seen)
	}
	p.OnLockScreenChange(nil)
	p.handleMeta(webrtc.DataChannelMessage{Data: []byte{metaLockScreen, byte(LockScreenUnlocked)}})
	if seen != 1 || p.LockScreen() != LockScreenUnlocked {
		t.Fatal("state was not updated after notification was removed")
	}
}

func TestLockScreenDiagnosticStatesRoundTrip(t *testing.T) {
	host, viewer := connect(t)
	changed := make(chan LockScreenState, 1)
	viewer.OnLockScreenChange(func() { changed <- viewer.LockScreen() })
	for state := LockScreenState(6); state <= 10; state++ {
		if err := host.SendLockScreen(state); err != nil {
			t.Fatalf("send state %d: %v", state, err)
		}
		select {
		case got := <-changed:
			if got != state {
				t.Fatalf("received state %d, want %d", got, state)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("state %d did not arrive", state)
		}
	}
	if err := host.SendLockScreen(11); err == nil {
		t.Fatal("accepted unknown lock screen state")
	}
}

func TestUnlockMetaRoundTrip(t *testing.T) {
	host, viewer := connect(t)
	requests, results := make(chan string, 1), make(chan UnlockResult, 1)
	host.OnUnlock(func(secret []byte) { requests <- string(secret) })
	viewer.OnUnlockResult(func(result UnlockResult) { results <- result })
	secret := []byte("test PIN")
	if err := viewer.SendUnlock(secret); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-requests:
		if got != string(secret) {
			t.Fatal("request was changed in transit")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("unlock request missing")
	}
	for _, want := range []UnlockResult{UnlockSucceeded, UnlockStillLocked, UnlockUnsupported, UnlockTooMany} {
		if err := host.SendUnlockResult(want); err != nil {
			t.Fatal(err)
		}
		select {
		case got := <-results:
			if got != want {
				t.Fatalf("got result %v, want %v", got, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("unlock result missing")
		}
	}
}

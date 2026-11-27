package app

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tangoui/kkdesk/internal/capture"
	"github.com/tangoui/kkdesk/internal/session"
)

// connectDesktop opens a desktop session to a host whose screen lock state
// and capture start are faked, and returns the reason the host closed it with.
func connectDesktop(t *testing.T, locked func() (bool, error), start func(capture.Options, func(capture.Frame)) (capture.Session, error)) (string, HostState) {
	t.Helper()
	old := captureStart
	captureStart = start
	t.Cleanup(func() { captureStart = old })
	h := NewHost(HostConfig{Addr: "127.0.0.1:0", Password: "123456", Pairing: testHostIdentity(t), AutoAccept: true,
		screenLocked: locked}, nil)
	defer h.Close()
	if err := h.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	p, err := session.Dial(ctx, h.ListenAddr(), "123456")
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	select {
	case <-p.Done():
	case <-ctx.Done():
		t.Fatal("the host kept the session open")
	}
	return p.Reason(), h.State()
}

func noCapture(t *testing.T) func(capture.Options, func(capture.Frame)) (capture.Session, error) {
	return func(capture.Options, func(capture.Frame)) (capture.Session, error) {
		t.Error("capture started on a locked screen")
		return nil, errors.New("unexpected capture")
	}
}

// A locked Windows session cannot be captured by a normal process: the
// controller is told so in words instead of a DXGI error code.
func TestLockedScreenRefusesWithAPlainReason(t *testing.T) {
	reason, st := connectDesktop(t, func() (bool, error) { return true, nil }, noCapture(t))
	if reason != screenLockedReason {
		t.Fatalf("reason %q, want %q", reason, screenLockedReason)
	}
	if !strings.Contains(st.Err, "锁屏") {
		t.Fatalf("host error %q does not mention the lock screen", st.Err)
	}
}

// The screen can lock between the check and the first frame: a capture
// failure on a now-locked screen gets the same reason.
func TestCaptureFailureOnALockedScreenGetsThePlainReason(t *testing.T) {
	var checks atomic.Int32
	locked := func() (bool, error) { return checks.Add(1) > 1, nil }
	fail := func(capture.Options, func(capture.Frame)) (capture.Session, error) {
		return nil, errors.New("DuplicateOutput: HRESULT 0x887A0002")
	}
	reason, _ := connectDesktop(t, locked, fail)
	if reason != screenLockedReason {
		t.Fatalf("reason %q, want %q", reason, screenLockedReason)
	}
}

// Other capture failures, and an unreadable lock state, keep the
// underlying error.
func TestCaptureFailureOnAnUnlockedScreenKeepsTheError(t *testing.T) {
	fail := func(capture.Options, func(capture.Frame)) (capture.Session, error) {
		return nil, errors.New("DuplicateOutput: HRESULT 0x887A0002")
	}
	for name, locked := range map[string]func() (bool, error){
		"unlocked":   func() (bool, error) { return false, nil },
		"unreadable": func() (bool, error) { return false, errors.New("WTSQuerySessionInformation failed") },
	} {
		t.Run(name, func(t *testing.T) {
			reason, _ := connectDesktop(t, locked, fail)
			if !strings.Contains(reason, "0x887A0002") || strings.Contains(reason, "锁屏") {
				t.Fatalf("reason %q, want the capture error", reason)
			}
		})
	}
}

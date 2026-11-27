package app

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/tangoui/kkdesk/internal/capture"
	"github.com/tangoui/kkdesk/internal/lockscreen"
	"github.com/tangoui/kkdesk/internal/session"
)

func noCapture(t *testing.T) func(capture.Options, func(capture.Frame)) (capture.Session, error) {
	return func(capture.Options, func(capture.Frame)) (capture.Session, error) {
		t.Error("capture started on a locked screen")
		return nil, errors.New("unexpected capture")
	}
}

func TestLockScreenReportsUnsupportedWithNoHelper(t *testing.T) {
	_, p := startTestLockHost(t, HostConfig{
		screenLocked: func() (bool, error) { return true, nil },
		startDesktop: noCapture(t),
		startLockScreen: func(context.Context, capture.Options, func(capture.Frame)) (lockscreen.Session, error) {
			return nil, lockscreen.ErrNotInstalled
		},
	})
	waitFor(t, "unsupported lock screen", func() bool { return lockScreenUnavailable(p.LockScreen()) })
}

func TestLockScreenReportsSignatureRequirement(t *testing.T) {
	_, p := startTestLockHost(t, HostConfig{
		screenLocked: func() (bool, error) { return true, nil },
		startDesktop: noCapture(t),
		startLockScreen: func(context.Context, capture.Options, func(capture.Frame)) (lockscreen.Session, error) {
			return nil, lockscreen.ErrNeedsSignature
		},
	})
	waitFor(t, "signature required", func() bool { return p.LockScreen() == session.LockScreenSignatureRequired })
}

func TestLockScreenReportsUnexpectedComponentFailure(t *testing.T) {
	_, p := startTestLockHost(t, HostConfig{
		screenLocked: func() (bool, error) { return true, nil },
		startDesktop: noCapture(t),
		startLockScreen: func(context.Context, capture.Options, func(capture.Frame)) (lockscreen.Session, error) {
			return nil, errors.New("map frames: access denied")
		},
	})
	waitFor(t, "lock screen diagnosis", func() bool { return p.LockScreen() != session.LockScreenUnknown })
	if got := p.LockScreen(); got != session.LockScreenComponentError {
		t.Fatalf("lock screen state = %d, want component error", got)
	}
}

func lockScreenUnavailable(state session.LockScreenState) bool {
	return state == session.LockScreenComponentUnavailable || state == session.LockScreenPlatformUnsupported ||
		state == session.LockScreenSignatureRequired
}

func TestCaptureFailureOnALockedScreenGetsThePlainReason(t *testing.T) {
	var checks atomic.Int32
	locked := func() (bool, error) { return checks.Add(1) > 1, nil }
	fail := func(capture.Options, func(capture.Frame)) (capture.Session, error) {
		return nil, errors.New("DuplicateOutput: HRESULT 0x887A0002")
	}
	_, p := startTestLockHost(t, HostConfig{screenLocked: locked, startDesktop: fail})
	waitFor(t, "unsupported lock screen", func() bool { return lockScreenUnavailable(p.LockScreen()) })
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
			_, p := startTestLockHost(t, HostConfig{screenLocked: locked, startDesktop: fail})
			waitFor(t, "capture error", func() bool {
				select {
				case <-p.Done():
					return true
				default:
					return false
				}
			})
			reason := p.Reason()
			if !strings.Contains(reason, "0x887A0002") || strings.Contains(reason, "锁屏") {
				t.Fatalf("reason %q, want the capture error", reason)
			}
		})
	}
}

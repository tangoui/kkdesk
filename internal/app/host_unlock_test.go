package app

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tangoui/kkdesk/internal/capture"
	"github.com/tangoui/kkdesk/internal/input"
	"github.com/tangoui/kkdesk/internal/lockscreen"
	"github.com/tangoui/kkdesk/internal/session"
)

type testLockCapture struct {
	closed atomic.Bool
}

func (*testLockCapture) SetFPS(int)           {}
func (*testLockCapture) Size() (int, int)     { return 0, 0 }
func (*testLockCapture) Stats() capture.Stats { return capture.Stats{} }
func (s *testLockCapture) Close()             { s.closed.Store(true) }

type testLockHelper struct {
	testLockCapture
	ended    chan struct{}
	inputs   atomic.Int32
	attempts atomic.Int32
	onUnlock func(context.Context) error
}

func (s *testLockHelper) Done() <-chan struct{} { return s.ended }
func (s *testLockHelper) Apply(input.Event) error {
	s.inputs.Add(1)
	return nil
}

func (s *testLockHelper) Unlock(ctx context.Context, secret []byte) error {
	s.attempts.Add(1)
	if s.onUnlock != nil {
		return s.onUnlock(ctx)
	}
	return nil
}

func startTestLockHost(t *testing.T, cfg HostConfig) (*Host, *session.Peer) {
	t.Helper()
	cfg.Addr, cfg.Password, cfg.Pairing, cfg.AutoAccept = "127.0.0.1:0", "123456", testHostIdentity(t), true
	cfg.captureHelp = func() string { return "" }
	h := NewHost(cfg, nil)
	t.Cleanup(h.Close)
	if err := h.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	p, err := session.Dial(ctx, h.ListenAddr(), "123456")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close() })
	return h, p
}

func TestLockScreenCaptureSwitch(t *testing.T) {
	var locked atomic.Bool
	var normalStarts atomic.Int32
	first := &testLockCapture{}
	helper := &testLockHelper{ended: make(chan struct{})}
	_, p := startTestLockHost(t, HostConfig{
		screenLocked: func() (bool, error) { return locked.Load(), nil },
		startDesktop: func(capture.Options, func(capture.Frame)) (capture.Session, error) {
			if normalStarts.Add(1) == 1 {
				return first, nil
			}
			return &testLockCapture{}, nil
		},
		startLockScreen: func(context.Context, capture.Options, func(capture.Frame)) (lockscreen.Session, error) {
			if !first.closed.Load() {
				t.Error("normal capture was still running when the helper started")
			}
			return helper, nil
		},
	})
	waitFor(t, "unlocked desktop", func() bool { return p.LockScreen() == session.LockScreenUnlocked })
	locked.Store(true)
	waitFor(t, "lock screen helper", func() bool { return p.LockScreen() == session.LockScreenReady })
	if err := p.SendInput(input.Event{Kind: input.Key, HID: 4, Down: true}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "helper input", func() bool { return helper.inputs.Load() == 1 })
	locked.Store(false)
	waitFor(t, "desktop restored", func() bool {
		return p.LockScreen() == session.LockScreenUnlocked && normalStarts.Load() == 2
	})
	if !helper.closed.Load() {
		t.Error("helper remained active after unlock")
	}
}

func TestUnlockHostLimitAndDisconnect(t *testing.T) {
	helper := &testLockHelper{ended: make(chan struct{})}
	helper.onUnlock = func(context.Context) error { return lockscreen.ErrStillLocked }
	h, p := startTestLockHost(t, HostConfig{
		screenLocked: func() (bool, error) { return true, nil },
		startDesktop: noCapture(t),
		startLockScreen: func(context.Context, capture.Options, func(capture.Frame)) (lockscreen.Session, error) {
			return helper, nil
		},
	})
	waitFor(t, "initial lock screen", func() bool { return p.LockScreen() == session.LockScreenReady })
	replies := make(chan session.UnlockResult, 4)
	p.OnUnlockResult(func(r session.UnlockResult) { replies <- r })
	for _, want := range []session.UnlockResult{
		session.UnlockStillLocked, session.UnlockStillLocked, session.UnlockStillLocked, session.UnlockTooMany,
	} {
		if err := p.SendUnlock([]byte("test-only-credential")); err != nil {
			t.Fatal(err)
		}
		select {
		case got := <-replies:
			if got != want {
				t.Fatalf("unlock result %v, want %v", got, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("missing unlock result")
		}
	}
	if helper.attempts.Load() != 3 || h.State().Notice == "" {
		t.Fatal("unlock quota was not enforced or reported")
	}
	_ = p.Close()
	waitFor(t, "helper closed", helper.closed.Load)
}

func TestUnlockHostResult(t *testing.T) {
	for _, tc := range []struct {
		name   string
		locked func() (bool, error)
		helper lockscreen.Session
		want   session.UnlockResult
	}{
		{"unlocked", func() (bool, error) { return false, nil }, nil, session.UnlockSucceeded},
		{"unknown", func() (bool, error) { return false, errors.New("unavailable") }, nil, session.UnlockUnsupported},
		{"no helper", func() (bool, error) { return true, nil }, nil, session.UnlockUnsupported},
		{"timeout", func() (bool, error) { return true, nil }, &testLockHelper{
			onUnlock: func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() },
		}, session.UnlockStillLocked},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := &Host{cfg: HostConfig{screenLocked: tc.locked, unlockTimeout: time.Millisecond}}
			attempts := 0
			got := h.tryUnlock(context.Background(), tc.helper, []byte("test-secret"), &attempts)
			if got != tc.want {
				t.Fatalf("result %v, want %v", got, tc.want)
			}
		})
	}
}

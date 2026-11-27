package app

import (
	"bytes"
	"context"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tangoui/kkdesk/internal/capture"
	"github.com/tangoui/kkdesk/internal/config"
	"github.com/tangoui/kkdesk/internal/privacy"
	"github.com/tangoui/kkdesk/internal/session"
)

type blockedCaptureClose struct {
	testLockCapture
	closing chan struct{}
	release chan struct{}
}

type blockedPrivacyHide struct {
	fakePrivacyScreen
	hiding  chan struct{}
	release chan struct{}
}

func (s *blockedPrivacyHide) Hide() {
	close(s.hiding)
	<-s.release
	s.fakePrivacyScreen.Hide()
}

func TestHostStopsCaptureWhenPrivacyCleanupStalls(t *testing.T) {
	screen := &blockedPrivacyHide{hiding: make(chan struct{}), release: make(chan struct{})}
	source := &testLockCapture{}
	h, p := startTestLockHost(t, HostConfig{
		teardownTimeout: 100 * time.Millisecond,
		PrivacyPolicy:   config.PrivacyAllow,
		privacyScreen:   func() privacy.Screen { return screen },
		screenLocked:    func() (bool, error) { return false, nil },
		startDesktop: func(capture.Options, func(capture.Frame)) (capture.Session, error) {
			return source, nil
		},
	})
	defer close(screen.release)
	waitFor(t, "desktop privacy handler", func() bool {
		h.mu.Lock()
		defer h.mu.Unlock()
		return h.privacy != nil
	})
	if err := p.SendPrivacy(session.PrivacyMessage{Kind: session.PrivacyRequest, Status: session.PrivacyOn}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "privacy screen", func() bool { return h.State().PrivacyScreen })
	_ = p.Close()
	select {
	case <-screen.hiding:
	case <-time.After(2 * time.Second):
		t.Fatal("privacy screen did not enter Hide")
	}
	waitFor(t, "capture to stop while privacy Hide is blocked", source.closed.Load)
	if got := h.State(); got.Phase != HostListening || got.Peer != "" {
		t.Fatalf("host still owns the stalled session: phase %v, peer %q", got.Phase, got.Peer)
	}
}

func (s *blockedCaptureClose) Close() {
	close(s.closing)
	<-s.release
	s.testLockCapture.Close()
}

func TestHostAcceptsNextPeerWhenCaptureCloseStalls(t *testing.T) {
	var logs bytes.Buffer
	var logMu sync.Mutex
	oldLog := log.Writer()
	log.SetOutput(writerFunc(func(b []byte) (int, error) {
		logMu.Lock()
		defer logMu.Unlock()
		return logs.Write(b)
	}))
	t.Cleanup(func() { log.SetOutput(oldLog) })
	first := &blockedCaptureClose{closing: make(chan struct{}), release: make(chan struct{})}
	var starts atomic.Int32
	h, p := startTestLockHost(t, HostConfig{
		teardownTimeout: 100 * time.Millisecond,
		screenLocked:    func() (bool, error) { return false, nil },
		startDesktop: func(capture.Options, func(capture.Frame)) (capture.Session, error) {
			if starts.Add(1) == 1 {
				return first, nil
			}
			return &testLockCapture{}, nil
		},
	})
	var release sync.Once
	unblock := func() { release.Do(func() { close(first.release) }) }
	defer unblock()
	waitFor(t, "first capture to start", func() bool { return p.LockScreen() == session.LockScreenUnlocked })
	_ = p.Close()
	select {
	case <-first.closing:
	case <-time.After(2 * time.Second):
		t.Fatal("capture did not enter Close")
	}
	waitFor(t, "the host to release the stalled session", func() bool { return h.State().Phase == HostListening })
	if state := h.State(); state.Phase != HostListening || state.Peer != "" {
		t.Fatalf("host still owns the stalled session: phase %v, peer %q", state.Phase, state.Peer)
	}
	waitFor(t, "goroutine stacks in the diagnostic log", func() bool {
		logMu.Lock()
		defer logMu.Unlock()
		diagnostic := logs.String()
		return strings.Contains(diagnostic, "blockedCaptureClose).Close") &&
			strings.Contains(diagnostic, "TestHostAcceptsNextPeerWhenCaptureCloseStalls")
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	next, err := session.Dial(ctx, h.ListenAddr(), "123456")
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	waitFor(t, "second capture to start", func() bool { return next.LockScreen() == session.LockScreenUnlocked })
	unblock()
	waitFor(t, "old capture to finish closing", first.closed.Load)
	if got := h.State(); got.Phase != HostActive || got.Peer == "" {
		t.Fatalf("old cleanup cleared the new session: phase %v, peer %q", got.Phase, got.Peer)
	}
}

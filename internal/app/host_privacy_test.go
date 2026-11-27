package app

import (
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/tangoui/kkdesk/internal/config"
	"github.com/tangoui/kkdesk/internal/session"
)

type fakePrivacyScreen struct {
	shown     bool
	exit      func(error)
	err       error
	countdown atomic.Int32
}

func (s *fakePrivacyScreen) Show(_ func([]uint32) error, exit func(error)) error {
	if s.err != nil {
		return s.err
	}
	s.shown, s.exit = true, exit
	return nil
}

func (s *fakePrivacyScreen) Hide() { s.shown = false }

func (s *fakePrivacyScreen) SetLockCountdown(seconds int) { s.countdown.Store(int32(seconds)) }

type privacyTestResult struct {
	status session.PrivacyStatus
	detail string
}

func testPrivacyScreen(policy config.PrivacyPolicy, screen *fakePrivacyScreen) (*hostPrivacy, *[]privacyTestResult) {
	var results []privacyTestResult
	p := newHostPrivacy(policy, screen, nil, func(status session.PrivacyStatus, detail string) {
		results = append(results, privacyTestResult{status, detail})
	}, func() {})
	return p, &results
}

func TestHostPrivacyPolicy(t *testing.T) {
	for _, tc := range []struct {
		policy config.PrivacyPolicy
		on     session.PrivacyStatus
		shown  bool
	}{
		{config.PrivacyDeny, session.PrivacyNotAllowed, false},
		{config.PrivacyAsk, session.PrivacyWaiting, false},
		{config.PrivacyAllow, session.PrivacyOn, true},
	} {
		t.Run(string(tc.policy), func(t *testing.T) {
			screen := &fakePrivacyScreen{}
			p, results := testPrivacyScreen(tc.policy, screen)
			p.set(true)
			if len(*results) != 1 || (*results)[0].status != tc.on || screen.shown != tc.shown {
				t.Fatalf("enable: results = %v, shown = %v", *results, screen.shown)
			}
			p.set(false)
			if len(*results) != 2 || (*results)[1].status != session.PrivacyOff || screen.shown || p.state().request != 0 {
				t.Fatalf("disable: state = %+v, results = %v, shown = %v", p.state(), *results, screen.shown)
			}
		})
	}
}

func TestHostPrivacyConsentExpiresAndRejectsStaleAnswer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		screen := &fakePrivacyScreen{}
		p, results := testPrivacyScreen(config.PrivacyAsk, screen)
		p.set(true)
		first := p.state().request
		time.Sleep(29 * time.Second)
		synctest.Wait()
		if p.state().request != first || first == 0 {
			t.Fatal("request did not stay pending until 30 seconds")
		}
		time.Sleep(time.Second)
		synctest.Wait()
		if screen.shown || p.state().request != 0 || (*results)[1].status != session.PrivacyDenied {
			t.Fatalf("expired request: state = %+v, results = %v", p.state(), *results)
		}
		p.set(true)
		second := p.state().request
		p.answer(first, true)
		if screen.shown || second == 0 || p.state().request != second {
			t.Fatal("stale answer changed a new request")
		}
		p.answer(second, true)
		if !screen.shown || (*results)[len(*results)-1].status != session.PrivacyOn {
			t.Fatal("answering the current request did not enable the screen")
		}
	})
}

func TestHostPrivacyLocalExitRequiresConsentAgain(t *testing.T) {
	screen := &fakePrivacyScreen{}
	p, results := testPrivacyScreen(config.PrivacyAllow, screen)
	p.set(true)
	screen.exit(nil)
	if screen.shown || (*results)[1].status != session.PrivacyLocalExit {
		t.Fatalf("local exit: shown = %v, results = %v", screen.shown, *results)
	}
	p.set(true)
	if screen.shown || (*results)[2].status != session.PrivacyWaiting {
		t.Fatalf("re-request: shown = %v, results = %v", screen.shown, *results)
	}
	p.set(false)
}

func TestHostPrivacyPolicyChange(t *testing.T) {
	for _, policy := range []config.PrivacyPolicy{config.PrivacyAsk, config.PrivacyAllow} {
		screen := &fakePrivacyScreen{}
		p, results := testPrivacyScreen(policy, screen)
		p.set(true)
		request := p.state().request
		p.setPolicy(config.PrivacyDeny)
		p.answer(request, true)
		if screen.shown || p.state().request != 0 || (*results)[len(*results)-1].status != session.PrivacyNotAllowed {
			t.Fatalf("denying policy %s: state = %+v, results = %v", policy, p.state(), *results)
		}
	}
	screen := &fakePrivacyScreen{}
	p, _ := testPrivacyScreen(config.PrivacyAllow, screen)
	p.set(true)
	p.setPolicy(config.PrivacyAsk)
	if !screen.shown {
		t.Fatal("switching to ask closed an enabled screen")
	}
	p.set(false)
}

func TestHostPrivacyScreenFailure(t *testing.T) {
	screen := &fakePrivacyScreen{err: errors.New("test failure")}
	p, results := testPrivacyScreen(config.PrivacyAllow, screen)
	p.set(true)
	if p.state().on || len(*results) != 1 || (*results)[0] != (privacyTestResult{session.PrivacyUnsupported, "test failure"}) {
		t.Fatalf("failed screen: state = %+v, results = %v", p.state(), *results)
	}
}

func TestHostPrivacyReadsCurrentPolicyAtRequest(t *testing.T) {
	screen := &fakePrivacyScreen{}
	p, results := testPrivacyScreen(config.PrivacyAllow, screen)
	p.readPolicy = func() config.PrivacyPolicy { return config.PrivacyAsk }
	p.set(true)
	if screen.shown || (*results)[0].status != session.PrivacyWaiting {
		t.Fatalf("used cached allow policy: shown=%v, results=%v", screen.shown, *results)
	}
	p.close()
}

type fakeLocker func() error

func (f fakeLocker) Lock() error { return f() }

func TestHostDisconnectLockGrace(t *testing.T) {
	for _, action := range []string{"timeout", "reconnect", "shortcut", "quit", "bye", "failure"} {
		t.Run(action, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				screen := &fakePrivacyScreen{}
				p, _ := testPrivacyScreen(config.PrivacyAllow, screen)
				p.set(true)
				var locks atomic.Int32
				h := NewHost(HostConfig{LockAfterDisconnect: true, locker: fakeLocker(func() error {
					if !screen.shown {
						t.Error("screen removed before locking")
					}
					locks.Add(1)
					if action == "failure" {
						return errors.New("system failure")
					}
					return nil
				})}, nil)
				graceful := action == "bye" || action == "failure"
				h.endDesktopPrivacy(p, graceful, nil)
				if !graceful {
					deadline := h.State().LockDeadline
					if time.Until(deadline) != time.Minute || !p.state().on || locks.Load() != 0 {
						t.Fatalf("started grace with deadline %v, shown %v, locks %d", deadline, p.state().on, locks.Load())
					}
					if screen.countdown.Load() != 60 {
						t.Fatal("screen did not display the initial grace countdown")
					}
					time.Sleep(59 * time.Second)
					synctest.Wait()
					if locks.Load() != 0 || !p.state().on {
						t.Fatal("grace ended early")
					}
					if seconds := screen.countdown.Load(); seconds != 1 {
						t.Fatalf("screen countdown after 59 seconds = %d", seconds)
					}
					switch action {
					case "reconnect":
						h.admitAfterGrace()
					case "shortcut":
						screen.exit(nil)
					case "quit":
						h.Close()
					}
					time.Sleep(time.Second)
					synctest.Wait()
				}
				want := int32(1)
				if action == "reconnect" || action == "shortcut" {
					want = 0
				}
				if locks.Load() != want || screen.shown || !h.State().LockDeadline.IsZero() {
					t.Fatalf("grace ended with %d locks (want %d), shown=%v, deadline=%v", locks.Load(), want, screen.shown, h.State().LockDeadline)
				}
				if action == "failure" && h.State().Err != "会话结束后锁屏失败：system failure" {
					t.Fatalf("missing lock error: %q", h.State().Err)
				}
			})
		})
	}
}

func TestHostLockAndDisconnectWaitsForConfirmation(t *testing.T) {
	for _, lockErr := range []error{nil, errors.New("lock refused")} {
		screen := &fakePrivacyScreen{}
		p, reports := testPrivacyScreen(config.PrivacyAllow, screen)
		p.set(true)
		started, confirm, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
		locker := fakeLocker(func() error {
			close(started)
			<-confirm
			return lockErr
		})
		var got session.PrivacyMessage
		var closed bool
		go func() {
			p.lockNow(locker, func(m session.PrivacyMessage) { got = m }, func() { closed = true })
			close(done)
		}()
		<-started
		if !screen.shown {
			t.Fatal("privacy screen was removed before system lock confirmation")
		}
		select {
		case <-done:
			t.Fatal("returned before confirmation")
		default:
		}
		close(confirm)
		<-done
		if lockErr == nil {
			if !closed || got.Status != session.PrivacyOn || !p.locked {
				t.Fatalf("successful lock: closed=%v status=%+v", closed, got)
			}
		} else {
			want := session.PrivacyMessage{Kind: session.LockNowResult, Status: session.PrivacyFailed, Detail: "lock refused"}
			if closed || !screen.shown || !p.state().on || p.locked || got != want {
				t.Fatalf("failed lock: closed=%v shown=%v state=%+v locked=%v result=%+v", closed, screen.shown, p.state(), p.locked, got)
			}
			if len(*reports) != 1 || (*reports)[0].status != session.PrivacyOn {
				t.Fatalf("failed lock sent a privacy state change: %+v", *reports)
			}
		}
		p.close()
	}
}

package app

import (
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/tangoui/kkdesk/internal/config"
)

type fakePrivacyScreen struct {
	shown bool
	exit  func(error)
	err   error
}

func (s *fakePrivacyScreen) Show(_ func([]uint32) error, exit func(error)) error {
	if s.err != nil {
		return s.err
	}
	s.shown, s.exit = true, exit
	return nil
}

func (s *fakePrivacyScreen) Hide() { s.shown = false }

type privacyTestResult struct {
	status privacyStatus
	detail string
}

func testPrivacyScreen(policy config.PrivacyPolicy, screen *fakePrivacyScreen) (*hostPrivacy, *[]privacyTestResult) {
	var results []privacyTestResult
	p := newHostPrivacy(policy, screen, nil, func(status privacyStatus, detail string) {
		results = append(results, privacyTestResult{status, detail})
	}, func() {})
	return p, &results
}

func TestHostPrivacyPolicy(t *testing.T) {
	for _, tc := range []struct {
		policy config.PrivacyPolicy
		on     privacyStatus
		shown  bool
	}{
		{config.PrivacyDeny, privacyNotAllowed, false},
		{config.PrivacyAsk, privacyWaiting, false},
		{config.PrivacyAllow, privacyOn, true},
	} {
		t.Run(string(tc.policy), func(t *testing.T) {
			screen := &fakePrivacyScreen{}
			p, results := testPrivacyScreen(tc.policy, screen)
			p.set(true)
			if len(*results) != 1 || (*results)[0].status != tc.on || screen.shown != tc.shown {
				t.Fatalf("enable: results = %v, shown = %v", *results, screen.shown)
			}
			p.set(false)
			if len(*results) != 2 || (*results)[1].status != privacyOff || screen.shown || p.state().request != 0 {
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
		if screen.shown || p.state().request != 0 || (*results)[1].status != privacyDenied {
			t.Fatalf("expired request: state = %+v, results = %v", p.state(), *results)
		}
		p.set(true)
		second := p.state().request
		p.answer(first, true)
		if screen.shown || second == 0 || p.state().request != second {
			t.Fatal("stale answer changed a new request")
		}
		p.answer(second, true)
		if !screen.shown || (*results)[len(*results)-1].status != privacyOn {
			t.Fatal("answering the current request did not enable the screen")
		}
	})
}

func TestHostPrivacyLocalExitRequiresConsentAgain(t *testing.T) {
	screen := &fakePrivacyScreen{}
	p, results := testPrivacyScreen(config.PrivacyAllow, screen)
	p.set(true)
	screen.exit(nil)
	if screen.shown || (*results)[1].status != privacyLocalExit {
		t.Fatalf("local exit: shown = %v, results = %v", screen.shown, *results)
	}
	p.set(true)
	if screen.shown || (*results)[2].status != privacyWaiting {
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
		if screen.shown || p.state().request != 0 || (*results)[len(*results)-1].status != privacyNotAllowed {
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
	if p.state().on || len(*results) != 1 || (*results)[0] != (privacyTestResult{privacyUnsupported, "test failure"}) {
		t.Fatalf("failed screen: state = %+v, results = %v", p.state(), *results)
	}
}

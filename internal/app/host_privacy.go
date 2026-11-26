package app

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/tangoui/kkdesk/internal/config"
	"github.com/tangoui/kkdesk/internal/privacy"
)

const privacyConsentTimeout = 30 * time.Second

type privacyStatus byte

const (
	privacyOff privacyStatus = iota
	privacyOn
	privacyWaiting
	privacyDenied
	privacyNotAllowed
	privacyUnsupported
	privacyLocalExit
)

type hostPrivacyState struct {
	on       bool
	request  uint64
	deadline time.Time
}

// hostPrivacy serializes native operations off the main thread. The UI
// reads a snapshot so it never blocks on a native call that needs AppKit.
type hostPrivacy struct {
	mu      sync.Mutex
	policy  config.PrivacyPolicy
	screen  privacy.Screen
	exclude func([]uint32) error
	report  func(privacyStatus, string)
	notify  func()

	current   atomic.Pointer[hostPrivacyState]
	requestID uint64
	consent   *time.Timer
	askAgain  bool
	showID    uint64
}

func newHostPrivacy(policy config.PrivacyPolicy, screen privacy.Screen, exclude func([]uint32) error,
	report func(privacyStatus, string), notify func()) *hostPrivacy {
	return &hostPrivacy{policy: policy, screen: screen, exclude: exclude, report: report, notify: notify}
}

func (p *hostPrivacy) state() hostPrivacyState {
	if s := p.current.Load(); s != nil {
		return *s
	}
	return hostPrivacyState{}
}

func (p *hostPrivacy) publish(state hostPrivacyState, status privacyStatus, detail string) {
	p.current.Store(&state)
	p.report(status, detail)
	p.notify()
}

func (p *hostPrivacy) set(on bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	state := p.state()
	switch {
	case !on:
		p.cancelConsent()
		p.hide()
		p.publish(hostPrivacyState{}, privacyOff, "")
	case p.policy == config.PrivacyDeny:
		p.publish(hostPrivacyState{}, privacyNotAllowed, "")
	case state.on:
		p.report(privacyOn, "")
	case state.request != 0:
		p.report(privacyWaiting, "")
	case p.policy == config.PrivacyAllow && !p.askAgain:
		p.show()
	default:
		p.requestID++
		id := p.requestID
		deadline := time.Now().Add(privacyConsentTimeout)
		p.consent = time.AfterFunc(time.Until(deadline), func() { p.answer(id, false) })
		p.publish(hostPrivacyState{request: id, deadline: deadline}, privacyWaiting, "")
	}
}

func (p *hostPrivacy) answer(id uint64, allow bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	state := p.state()
	if id == 0 || id != state.request {
		return
	}
	p.cancelConsent()
	if !allow || !time.Now().Before(state.deadline) {
		p.publish(hostPrivacyState{}, privacyDenied, "")
		return
	}
	p.show()
}

func (p *hostPrivacy) setPolicy(policy config.PrivacyPolicy) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.policy = policy
	s := p.state()
	if policy == config.PrivacyDeny && (s.on || s.request != 0) {
		p.cancelConsent()
		p.hide()
		p.publish(hostPrivacyState{}, privacyNotAllowed, "")
	}
}

func (p *hostPrivacy) cancelConsent() {
	if p.consent != nil {
		p.consent.Stop()
		p.consent = nil
	}
}

func (p *hostPrivacy) show() {
	p.showID++
	id := p.showID
	if err := p.screen.Show(p.exclude, func(err error) { p.exited(id, err) }); err != nil {
		p.publish(hostPrivacyState{}, privacyUnsupported, err.Error())
		return
	}
	p.publish(hostPrivacyState{on: true}, privacyOn, "")
}

func (p *hostPrivacy) hide() {
	if p.state().on {
		p.screen.Hide()
	}
}

func (p *hostPrivacy) exited(id uint64, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.showID != id || !p.state().on {
		return
	}
	p.hide()
	if err != nil {
		p.publish(hostPrivacyState{}, privacyUnsupported, err.Error())
		return
	}
	p.askAgain = true
	p.publish(hostPrivacyState{}, privacyLocalExit, "")
}

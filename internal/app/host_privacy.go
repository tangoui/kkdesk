package app

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/tangoui/kkdesk/internal/config"
	"github.com/tangoui/kkdesk/internal/privacy"
	"github.com/tangoui/kkdesk/internal/session"
)

const privacyConsentTimeout = 30 * time.Second

type hostPrivacyState struct {
	on        bool
	lockAfter bool
	request   uint64
	deadline  time.Time
}

// hostPrivacy serializes native operations off the main thread. The UI
// reads a snapshot so it never blocks on a native call that needs AppKit.
type hostPrivacy struct {
	mu      sync.Mutex
	policy  config.PrivacyPolicy
	screen  privacy.Screen
	exclude func([]uint32) error
	report  func(session.PrivacyStatus, string)
	notify  func()

	current   atomic.Pointer[hostPrivacyState]
	requestID uint64
	consent   *time.Timer
	askAgain  bool
	showID    uint64
	closed    bool
	locked    bool
}

func newHostPrivacy(policy config.PrivacyPolicy, screen privacy.Screen, exclude func([]uint32) error,
	report func(session.PrivacyStatus, string), notify func()) *hostPrivacy {
	return &hostPrivacy{policy: policy, screen: screen, exclude: exclude, report: report, notify: notify}
}

func (p *hostPrivacy) state() hostPrivacyState {
	if s := p.current.Load(); s != nil {
		return *s
	}
	return hostPrivacyState{}
}

func (p *hostPrivacy) publish(state hostPrivacyState, status session.PrivacyStatus, detail string) {
	state.lockAfter = p.state().lockAfter
	p.current.Store(&state)
	p.report(status, detail)
	p.notify()
}

func (p *hostPrivacy) set(on bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	state := p.state()
	switch {
	case !on:
		p.cancelConsent()
		p.hide()
		p.publish(hostPrivacyState{}, session.PrivacyOff, "")
	case p.policy == config.PrivacyDeny:
		p.publish(hostPrivacyState{}, session.PrivacyNotAllowed, "")
	case state.on:
		p.report(session.PrivacyOn, "")
	case state.request != 0:
		p.report(session.PrivacyWaiting, "")
	case p.policy == config.PrivacyAllow && !p.askAgain:
		p.show()
	default:
		p.requestID++
		id := p.requestID
		deadline := time.Now().Add(privacyConsentTimeout)
		p.consent = time.AfterFunc(time.Until(deadline), func() { p.answer(id, false) })
		p.publish(hostPrivacyState{request: id, deadline: deadline}, session.PrivacyWaiting, "")
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
		p.publish(hostPrivacyState{}, session.PrivacyDenied, "")
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
		p.publish(hostPrivacyState{}, session.PrivacyNotAllowed, "")
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
		p.publish(hostPrivacyState{}, session.PrivacyUnsupported, err.Error())
		return
	}
	p.publish(hostPrivacyState{on: true}, session.PrivacyOn, "")
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
		p.publish(hostPrivacyState{}, session.PrivacyUnsupported, err.Error())
		return
	}
	p.askAgain = true
	p.publish(hostPrivacyState{}, session.PrivacyLocalExit, "")
}

func (p *hostPrivacy) handle(peer *session.Peer, m session.PrivacyMessage, locker privacy.Locker) {
	switch m.Kind {
	case session.PrivacyRequest:
		p.set(m.Status == session.PrivacyOn)
	case session.LockOnDisconnectRequest:
		p.mu.Lock()
		s := p.state()
		s.lockAfter = m.Status == session.PrivacyOn
		p.current.Store(&s)
		p.mu.Unlock()
		_ = peer.SendPrivacy(session.PrivacyMessage{Kind: session.LockOnDisconnectState, Status: m.Status})
		p.notify()
	case session.LockNowRequest:
		p.lockNow(locker, func(result session.PrivacyMessage) { _ = peer.SendPrivacy(result) },
			func() { _ = peer.CloseWithReason("对方已锁屏") })
	}
}

func (p *hostPrivacy) close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	p.cancelConsent()
	p.hide()
	p.current.Store(nil)
}

func (p *hostPrivacy) lockNow(locker privacy.Locker, report func(session.PrivacyMessage), disconnect func()) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.locked {
		return
	}
	result := session.PrivacyMessage{Kind: session.LockNowResult, Status: session.PrivacyOn}
	if err := locker.Lock(); err != nil {
		if p.state().on {
			p.hide()
			p.publish(hostPrivacyState{}, session.PrivacyOff, "")
		}
		result.Status, result.Detail = session.PrivacyFailed, err.Error()
		report(result)
		return
	}
	p.locked = true
	report(result)
	disconnect()
}

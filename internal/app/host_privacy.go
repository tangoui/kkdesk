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

var privacyRequestID atomic.Uint64

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

	current     atomic.Pointer[hostPrivacyState]
	consent     *time.Timer
	askAgain    bool
	showID      uint64
	closed      bool
	locked      bool
	detached    atomic.Bool
	cancelGrace func()
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
		id := privacyRequestID.Add(1)
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
	p.updatePolicy(func() config.PrivacyPolicy { return policy })
}

func (p *hostPrivacy) updatePolicy(policy func() config.PrivacyPolicy) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.policy = policy()
	s := p.state()
	if p.policy == config.PrivacyDeny && (s.on || s.request != 0) {
		p.cancelConsent()
		p.hide()
		p.publish(hostPrivacyState{}, session.PrivacyNotAllowed, "")
	}
}

// SetPrivacyPolicy changes permission for the current and future sessions.
func (h *Host) SetPrivacyPolicy(policy config.PrivacyPolicy) {
	h.mu.Lock()
	h.cfg.PrivacyPolicy = policy
	active, grace := h.privacy, h.grace
	h.mu.Unlock()
	currentPolicy := func() config.PrivacyPolicy {
		h.mu.Lock()
		defer h.mu.Unlock()
		return h.cfg.PrivacyPolicy
	}
	if active != nil {
		go active.updatePolicy(currentPolicy)
	}
	if grace != nil && grace.session != active {
		go grace.session.updatePolicy(currentPolicy)
	}
}

func (h *Host) SetAutoLockAfterDisconnect(on bool) {
	h.mu.Lock()
	h.cfg.LockAfterDisconnect = on
	h.mu.Unlock()
	h.notify()
}

func (h *Host) AnswerPrivacy(id uint64, allow bool) {
	h.mu.Lock()
	p := h.privacy
	h.mu.Unlock()
	if p != nil {
		go p.answer(id, allow)
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
	if p.showID != id || !p.state().on {
		p.mu.Unlock()
		return
	}
	if p.cancelGrace != nil && err == nil {
		cancel := p.cancelGrace
		p.mu.Unlock()
		cancel()
		return
	}
	defer p.mu.Unlock()
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
	_ = p.finish(nil)
}

func (p *hostPrivacy) finish(locker privacy.Locker) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	p.cancelGrace = nil
	p.cancelConsent()
	var err error
	if locker != nil && !p.locked {
		err = locker.Lock()
	}
	p.hide()
	p.current.Store(nil)
	return err
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

const lockGracePeriod = 60 * time.Second

type privacyGrace struct {
	session *hostPrivacy
	until   time.Time
	timer   *time.Timer
	done    chan struct{}
}

func (h *Host) endDesktopPrivacy(p *hostPrivacy, graceful bool) {
	h.graceMu.Lock()
	defer h.graceMu.Unlock()
	h.mu.Lock()
	lock := h.cfg.LockAfterDisconnect || p.state().lockAfter
	graceful = graceful || h.closed || h.privacyDisconnect
	h.mu.Unlock()
	if !lock || graceful || p.locked {
		h.finishDesktopPrivacy(p, lock)
		return
	}

	g := &privacyGrace{session: p, until: time.Now().Add(lockGracePeriod), done: make(chan struct{})}
	p.mu.Lock()
	p.closed = true
	p.detached.Store(true)
	p.cancelConsent()
	p.cancelGrace = func() { h.finishGrace(g, false) }
	s := p.state()
	s.request, s.deadline = 0, time.Time{}
	p.current.Store(&s)
	if s.on {
		p.screen.SetLockCountdown(int(lockGracePeriod / time.Second))
	}
	p.mu.Unlock()
	h.mu.Lock()
	h.grace = g
	h.mu.Unlock()
	g.timer = time.AfterFunc(time.Until(g.until), func() { h.finishGrace(g, true) })
	if s.on {
		go g.countDown()
	}
	h.notify()
}

func (g *privacyGrace) countDown() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-g.done:
			return
		case <-ticker.C:
			p := g.session
			p.mu.Lock()
			if !p.state().on {
				p.mu.Unlock()
				return
			}
			p.screen.SetLockCountdown(max(0, int((time.Until(g.until)+time.Second-1)/time.Second)))
			p.mu.Unlock()
		}
	}
}

func (h *Host) finishDesktopPrivacy(p *hostPrivacy, lock bool) {
	var locker privacy.Locker
	if lock {
		locker = h.cfg.locker
	}
	if err := p.finish(locker); err != nil {
		h.setErr("会话结束后锁屏失败：" + err.Error())
	}
}

// finishGrace serializes a reconnect, local cancellation, timer and app
// shutdown. A stale timer or shortcut can only act on its own session.
func (h *Host) finishGrace(expected *privacyGrace, lock bool) {
	h.graceMu.Lock()
	defer h.graceMu.Unlock()
	h.mu.Lock()
	g := h.grace
	if g == nil || (expected != nil && g != expected) {
		h.mu.Unlock()
		return
	}
	g.timer.Stop()
	close(g.done)
	h.grace = nil
	h.mu.Unlock()
	h.finishDesktopPrivacy(g.session, lock)
	h.notify()
}

// CancelPendingLock is the local action used while the grace countdown is visible.
func (h *Host) CancelPendingLock() {
	h.mu.Lock()
	g := h.grace
	h.mu.Unlock()
	if g != nil {
		go h.finishGrace(g, false)
	}
}

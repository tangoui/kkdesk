package app

import "github.com/tangoui/kkdesk/internal/session"

type viewerPrivacy struct {
	on, lockAfter, pending bool
	message                string
}

func privacyStatus(on bool) session.PrivacyStatus {
	if on {
		return session.PrivacyOn
	}
	return session.PrivacyOff
}

func (v *Viewer) SetPrivacyScreen(on bool) {
	v.sendPrivacy(session.PrivacyMessage{Kind: session.PrivacyRequest, Status: privacyStatus(on)})
}

func (v *Viewer) SetLockAfterDisconnect(on bool) {
	v.sendPrivacy(session.PrivacyMessage{Kind: session.LockOnDisconnectRequest, Status: privacyStatus(on)})
}

func (v *Viewer) LockAndDisconnect() {
	v.sendPrivacy(session.PrivacyMessage{Kind: session.LockNowRequest})
}

func (v *Viewer) sendPrivacy(m session.PrivacyMessage) {
	v.mu.Lock()
	if v.peer == nil || v.cfg.FilesOnly || v.phase != ViewerStreaming {
		v.mu.Unlock()
		return
	}
	v.privacy.message = ""
	if err := v.peer.SendPrivacy(m); err != nil {
		v.privacy.message = "请求发送失败：" + err.Error()
	}
	v.mu.Unlock()
	v.notify()
}

func (v *Viewer) handlePrivacy(m session.PrivacyMessage) {
	v.mu.Lock()
	switch m.Kind {
	case session.PrivacyState:
		v.privacy.on = m.Status == session.PrivacyOn
		v.privacy.pending = m.Status == session.PrivacyWaiting
		switch m.Status {
		case session.PrivacyWaiting:
			v.privacy.message = "等待对方同意…"
		case session.PrivacyDenied:
			v.privacy.message = "对方拒绝开启隐私屏"
		case session.PrivacyNotAllowed:
			v.privacy.message = "对方不允许开启隐私屏"
		case session.PrivacyUnsupported:
			v.privacy.message = "对方无法开启隐私屏：" + m.Detail
		case session.PrivacyLocalExit:
			v.privacy.message = "对方关闭了隐私屏"
		default:
			v.privacy.message = ""
		}
	case session.LockOnDisconnectState:
		v.privacy.lockAfter = m.Status == session.PrivacyOn
		v.privacy.message = m.Detail
	case session.LockNowResult:
		if m.Status == session.PrivacyOn {
			v.privacy.message = "对方已锁屏"
		} else {
			v.privacy.message = "对方电脑锁屏失败：" + m.Detail
		}
	}
	v.mu.Unlock()
	v.notify()
}

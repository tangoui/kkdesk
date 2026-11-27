package appui

import (
	"github.com/tangoui/tango/sys/menu"
	"github.com/tangoui/tango/ui"
	"github.com/tangoui/tango/ui/layout"

	"github.com/tangoui/kkdesk/internal/app"
	"github.com/tangoui/kkdesk/internal/session"
)

func lockScreenReason(state session.LockScreenState) string {
	switch state {
	case session.LockScreenReady:
		return "对方电脑已锁屏"
	case session.LockScreenComponentUnavailable:
		return "对方电脑未安装锁屏远控组件"
	case session.LockScreenPlatformUnsupported:
		return "对方系统暂不支持锁屏远程操作"
	default:
		return ""
	}
}

func (w *sessionWin) addUnlockMenu(m *menu.Menu, st app.ViewerState) {
	if w.v.FilesOnly() || st.ViewOnly || st.Phase == app.ViewerClosed || lockScreenReason(st.LockScreen) == "" {
		return
	}
	ready := st.LockScreen == session.LockScreenReady && !st.UnlockPending
	m.Add("解锁电脑…", func() {
		go func() {
			sent, err := w.v.RequestSavedUnlock()
			w.app.Post(func() {
				w.setUnlockError(err)
				if err == nil && !sent {
					w.openUnlockDialog()
				}
			})
		}()
	}).SetDisabled(!ready)
	if st.UnlockSavedFailed {
		m.Add("重新输入", func() { w.app.Post(w.openUnlockDialog) }).SetDisabled(!ready)
	}
	if st.CanRememberUnlock {
		m.Add("忘记解锁密码", func() {
			go func() {
				err := w.v.ForgetUnlock()
				w.app.Post(func() { w.setUnlockError(err) })
			}()
		}).SetDisabled(st.UnlockPending)
	}
}

func (w *sessionWin) setUnlockError(err error) {
	w.unlockError = ""
	if err != nil {
		w.unlockError = err.Error()
	}
	w.invalidate()
}

func (w *sessionWin) openUnlockDialog() {
	w.unlockOpen, w.unlockRemember, w.unlockSecret = true, false, ""
	w.focused = false
	w.invalidate()
}

func (w *sessionWin) unlockDialog(c *ui.Context, st app.ViewerState) {
	if !w.unlockOpen {
		return
	}
	if st.ViewOnly || st.LockScreen != session.LockScreenReady || st.Phase == app.ViewerClosed {
		w.unlockOpen, w.unlockSecret, w.unlockRemember = false, "", false
		return
	}
	open := true
	var submit bool
	md := ui.Modal(c, &open, "解锁电脑").Width(min(420, c.Size().W-32)).OkText("解锁").Body(func() {
		ui.Column(c, func() {
			const label = "对方电脑的登录密码或 PIN"
			submit = ui.Input(c, &w.unlockSecret).Password().Placeholder(label).A11yLabel(label).Submitted()
			if st.CanRememberUnlock {
				ui.Checkbox(c, "记住密码（保存在本机钥匙串）", &w.unlockRemember)
			}
		}, layout.Gap(8))
	})
	switch {
	case md.Ok() || submit:
		if w.unlockSecret == "" {
			return
		}
		secret, remember := []byte(w.unlockSecret), w.unlockRemember && st.CanRememberUnlock
		w.unlockOpen, w.unlockSecret, w.unlockRemember = false, "", false
		go func() {
			defer clear(secret)
			err := w.v.RequestUnlock(secret, remember)
			w.app.Post(func() { w.setUnlockError(err) })
		}()
	case md.Cancelled() || !open:
		w.unlockOpen, w.unlockSecret, w.unlockRemember = false, "", false
	}
}

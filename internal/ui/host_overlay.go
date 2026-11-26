package appui

import (
	"fmt"
	"time"

	"github.com/tangoui/tango/ui"
	"github.com/tangoui/tango/ui/a11y"
	"github.com/tangoui/tango/ui/icon/antd"
	"github.com/tangoui/tango/ui/input"
	"github.com/tangoui/tango/ui/layout"

	"github.com/tangoui/kkdesk/internal/app"
)

// Permissions shown on the confirmation and the controlled panel.
// 键盘鼠标 is always on; 剪贴板 and 文件传输 are the host's switches; 声音
// does not exist yet and is shown off.
const (
	permClipboard = "剪贴板"
	permFiles     = "文件传输"
)

var perms = []struct {
	label string
	on    bool
}{{"键盘鼠标", true}, {permClipboard, true}, {permFiles, true}, {"声音", false}}

// permState is whether a permission is on and whether the host's user can
// switch it.
func permState(hs app.HostState, label string, fixed bool) (on, toggle bool) {
	switch label {
	case permClipboard:
		return hs.ClipboardAccess, true
	case permFiles:
		return hs.FileAccess, true
	}
	return fixed, false
}

// setPerm switches a permission (off the UI thread: it may stop transfers).
func (m *Main) setPerm(label string, on bool) {
	switch label {
	case permClipboard:
		go m.o.Host.SetClipboardAccess(on)
	case permFiles:
		go m.o.Host.SetFileAccess(on)
	}
}

// permShown reports whether a permission applies to the session: a
// files-only session shares no screen, so only 文件传输 is shown.
func permShown(hs app.HostState, label string) bool {
	return !hs.FilesOnly || label == permFiles
}

// confirm is the incoming-connection dialog.
func (m *Main) confirm(c *ui.Context, hs app.HostState) {
	open := hs.Phase == app.HostPending
	if !open {
		return
	}
	th := c.Theme
	left := max(0, int(time.Until(hs.Deadline).Round(time.Second)/time.Second))
	c.WakeAt(time.Now().Add(time.Second))
	var accept, reject bool
	what, warn := "请求控制本机", "接受后对方可以看到你的屏幕。不认识的人请拒绝。"
	if hs.FilesOnly {
		what, warn = "请求传输文件", "接受后对方可以浏览本机文件并传输文件。不认识的人请拒绝。"
	}
	md := ui.Modal(c, &open, "连接请求").Width(min(420, c.Size().W-32)).MaskClosable(false).Header(func() {
		ui.Row(c, func() {
			peerIcon := antd.DesktopOutlined
			if hs.PhonePeer {
				peerIcon = antd.MobileOutlined
			}
			ui.Avatar(c, "").Icon(peerIcon).Size(44).Bg(th.Primary.Bg)
			ui.Column(c, func() {
				ui.Text(c, hs.Peer+" "+what).Size(16).Strong()
				if !hs.RemotePeer && !hs.PhonePeer {
					ui.Text(c, "地址 "+hs.Peer+" · 局域网直连").Size(12).Tertiary()
				}
			}, layout.Gap(2), layout.Grow(1), layout.MinW(0))
		}, layout.Gap(12), layout.Middle())
	}).Footer(func() {
		reject, accept = confirmFooter(c, "拒绝", "接受", "自动拒绝", left)
	}).Body(func() {
		ui.Column(c, func() {
			ui.Alert(c, "warning", warn).ShowIcon(true).Show()
			if hs.BiometricEnrollment {
				ui.Alert(c, "info", "接受后将登记这台手机的生物识别密钥。请确认手机刚完成面容或指纹验证。").ShowIcon(true).Show()
			}
			ui.Row(c, func() {
				for _, p := range perms {
					if !permShown(hs, p.label) {
						continue
					}
					on, toggle := permState(hs, p.label, p.on)
					if ui.Checkbox(c, p.label, &on).Disabled(!toggle).Changed() {
						m.setPerm(p.label, on)
					}
				}
			}, layout.GapXY(16, 8), layout.WrapItems())
			if !hs.FilesOnly {
				ui.Text(c, "声音暂未支持。剪贴板只同步文字。").Size(12).Tertiary()
			}
		}, layout.Gap(14), layout.Padding(insets(4, 0, 0, 0)))
	})
	switch {
	case accept:
		go m.o.Host.AcceptRequest(hs.Request)
	case reject || md.Cancelled() || !open:
		go m.o.Host.RejectRequest(hs.Request)
	}
}

func privacyConsentDialog(c *ui.Context, deadline time.Time) (allow, reject bool) {
	open := true
	left := max(0, int((time.Until(deadline)+time.Second-1)/time.Second))
	c.WakeAt(time.Now().Add(time.Second))
	dialog := ui.Modal(c, &open, "隐私屏请求").Width(min(420, c.Size().W-32)).MaskClosable(false).
		Footer(func() {
			reject, allow = confirmFooter(c, "拒绝", "允许", "自动拒绝", left)
		}).Body(func() {
		ui.Alert(c, "warning", "对方请求开启隐私屏。你的屏幕会变黑，对方仍能看到并操作你的电脑。").ShowIcon(true).Show()
	})
	return allow, reject || dialog.Cancelled() || !open
}

func lockGraceText(remaining time.Duration) string {
	if remaining <= 0 {
		return "正在锁屏…"
	}
	return fmt.Sprintf("连接已断开，%d 秒后锁屏", (remaining+time.Second-1)/time.Second)
}

func (m *Main) lockGrace(c *ui.Context, deadline time.Time) {
	c.WakeAt(time.Now().Add(time.Second))
	ui.Row(c, func() {
		ui.Alert(c, "warning", lockGraceText(time.Until(deadline))).ShowIcon(true).Show()
		if ui.Button(c, "取消锁屏").Small().Clicked() {
			m.o.Host.CancelPendingLock()
		}
	}, layout.Middle(), layout.WrapItems(), layout.Gap(8))
}

// controlledPanel is the bottom-right card shown while being controlled.
func (m *Main) controlledPanel(c *ui.Context, hs app.HostState) {
	th := c.Theme
	el := time.Since(hs.Since).Truncate(time.Second)
	c.WakeAt(time.Now().Add(time.Second))
	ui.Column(c, func() {
		ui.Row(c, func() {
			pulseDot(c, th.Error.Base, 8)
			what := " 正在控制本机"
			if hs.FilesOnly {
				what = " 正在传输文件"
			}
			ui.Text(c, hs.Peer+what).Strong().Ellipsis().Layout(layout.Grow(1))
			mono(c, formatElapsed(el), 12, 400, th.TextTertiary)
		}, layout.Gap(8), layout.Middle())
		ui.Row(c, func() {
			for _, p := range perms {
				if !permShown(hs, p.label) {
					continue
				}
				on, toggle := permState(hs, p.label, p.on)
				if permChip(c, p.label, on, toggle) {
					m.setPerm(p.label, !on)
				}
			}
		}, layout.GapXY(6, 6), layout.WrapItems())
		if hs.PrivacyScreen {
			ui.Text(c, "隐私屏已开启").Size(12).Secondary()
		}
		if hs.LockAfterDisconnect {
			ui.Text(c, "本次会话结束后将锁屏").Size(12).Secondary()
		}
		ui.Row(c, func() {
			ui.Spacer(c)
			if ui.Button(c, "断开").AutoInsertSpace(false).Danger().Small().Clicked() {
				go m.o.Host.Disconnect()
			}
		}, layout.Middle())
	}, layout.Abs(layout.Unset, layout.Unset, 16, 16), layout.W(280), layout.Pad(12), layout.Gap(10)).
		Bg(th.BgElevated).Radius(th.RadiusLG).Shadow(th.ShadowSecondary).Border(1, th.Error.Border)
}

func formatElapsed(d time.Duration) string {
	s := int(d / time.Second)
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, s/60%60, s%60)
	}
	return fmt.Sprintf("%02d:%02d", s/60, s%60)
}

// permChip is a permission chip of the controlled panel (prototype
// .perm-chip): blue when on, struck through when off. A toggle chip
// reports clicks; the others are display only.
func permChip(c *ui.Context, label string, on, toggle bool) bool {
	th := c.Theme
	b := ui.Row(c, func() {
		t := ui.Text(c, label).Size(12).NoWrap()
		if on {
			t.Color(th.Primary.Base)
		} else {
			t.Tertiary().Delete()
		}
	}, layout.H(24), layout.PadVH(0, 8), layout.Middle(), layout.NoStretch()).Radius(12)
	if on {
		b.Bg(th.Primary.Bg).Border(1, th.Primary.Border)
	} else {
		b.Bg(th.BgContainer).Border(1, th.Border)
	}
	if !toggle {
		return false
	}
	b.Semantics(a11y.RoleSwitch, label)
	ia := b.Interact(input.CursorPointer)
	if ia.Hovered {
		b.Border(1, th.Primary.Hover)
	}
	if on {
		b.A11yState(a11y.Checked)
	}
	return ia.Clicked
}

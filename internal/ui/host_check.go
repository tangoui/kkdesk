package appui

import (
	"context"

	"github.com/tangoui/tango/ui"
	"github.com/tangoui/tango/ui/layout"
)

// hostCheck is a first-connection device-code check waiting for the user.
type hostCheck struct {
	addr, code string
	reply      chan bool // buffered: the dialog never blocks on it
}

// confirmHost is the viewers' ConfirmHost: it shows the check and waits for
// the user (or ctx). Off the UI thread.
func (m *Main) confirmHost(ctx context.Context, addr, code string) bool {
	hc := &hostCheck{addr: addr, code: code, reply: make(chan bool, 1)}
	m.mu.Lock()
	if m.hostCheck != nil { // one check at a time; the other waits on the user
		m.mu.Unlock()
		return false
	}
	m.hostCheck = hc
	m.mu.Unlock()
	m.Invalidate()
	defer func() {
		m.mu.Lock()
		if m.hostCheck == hc {
			m.hostCheck = nil
		}
		m.mu.Unlock()
		m.Invalidate()
	}()
	select {
	case ok := <-hc.reply:
		return ok
	case <-ctx.Done():
		return false
	}
}

// hostCheckDialog shows a pending device-code check and reports whether it
// did. UI thread.
func (m *Main) hostCheckDialog(c *ui.Context) bool {
	m.mu.Lock()
	hc := m.hostCheck
	m.mu.Unlock()
	if hc == nil {
		return false
	}
	open := true
	var ok, cancel bool
	md := ui.Modal(c, &open, "核对对方设备码").Width(min(440, c.Size().W-32)).MaskClosable(false).Footer(func() {
		ui.Row(c, func() {
			ui.Spacer(c)
			if hc.code == "" { // nothing to compare: cancelling is the safe default
				ok = ui.Button(c, "仍然连接").AutoInsertSpace(false).Danger().Clicked()
				cancel = ui.Button(c, "取消").AutoInsertSpace(false).Primary().Clicked()
				return
			}
			cancel = ui.Button(c, "取消").AutoInsertSpace(false).Clicked()
			ok = ui.Button(c, "一致，继续连接").AutoInsertSpace(false).Primary().Clicked()
		}, layout.Gap(8), layout.Middle())
	}).Body(func() {
		ui.Column(c, func() {
			if hc.code == "" {
				ui.Text(c, "第一次连接 "+hc.addr+"，对方没有提供设备码。")
				ui.Alert(c, "error", "可能是对方的 kkdesk 版本太旧，也可能有人冒充那台电脑。无法核对时请取消，请对方升级后再连。").ShowIcon(true).Show()
				return
			}
			ui.Text(c, "第一次连接 "+hc.addr+"，对方的设备码是：")
			ui.Text(c, hc.code).Size(24).Strong()
			ui.Alert(c, "warning", "请对照对方电脑 kkdesk「连接」页显示的「本机设备码」，完全一致才继续。不一致可能有人冒充那台电脑，请取消。").ShowIcon(true).Show()
		}, layout.Gap(12), layout.Padding(insets(4, 0, 0, 0)))
	})
	if ok || cancel || md.Cancelled() || !open {
		select {
		case hc.reply <- ok:
		default:
		}
		m.mu.Lock()
		if m.hostCheck == hc {
			m.hostCheck = nil
		}
		m.mu.Unlock()
	}
	return true
}

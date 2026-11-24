package appui

import (
	"fmt"
	"time"

	"github.com/tangoui/tango/ui"
	"github.com/tangoui/tango/ui/icon/antd"
	"github.com/tangoui/tango/ui/layout"

	"mmdesk/internal/app"
)

// Permissions shown on the confirmation and the controlled panel. Only
// 键盘鼠标 exists today; the others are shown off (display only).
var perms = []struct {
	label string
	on    bool
}{{"键盘鼠标", true}, {"剪贴板", false}, {"文件传输", false}, {"声音", false}}

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
	md := ui.Modal(c, &open, "连接请求").Width(min(420, c.Size().W-32)).MaskClosable(false).Header(func() {
		ui.Row(c, func() {
			ui.Avatar(c, "").Icon(antd.DesktopOutlined).Size(44).Bg(th.Primary.Bg)
			ui.Column(c, func() {
				ui.Text(c, hs.Peer+" 请求控制本机").Size(16).Strong()
				ui.Text(c, "地址 "+hs.Peer+" · 局域网直连").Size(12).Tertiary()
			}, layout.Gap(2), layout.Grow(1), layout.MinW(0))
		}, layout.Gap(12), layout.Middle())
	}).Footer(func() {
		ui.Row(c, func() {
			reject = ui.Button(c, "拒绝").AutoInsertSpace(false).Clicked()
			accept = ui.Button(c, fmt.Sprintf("接受（%d）", left)).Primary().Clicked()
		}, layout.Gap(8), layout.JustifyContent(layout.JustifyEnd))
	}).Body(func() {
		ui.Column(c, func() {
			ui.Alert(c, "warning", "接受后对方可以看到你的屏幕。不认识的人请拒绝。").ShowIcon(true).Show()
			ui.Row(c, func() {
				for _, p := range perms {
					on := p.on
					ui.Checkbox(c, p.label, &on).Disabled(true)
				}
			}, layout.GapXY(16, 8), layout.WrapItems())
			ui.Text(c, "剪贴板、文件传输和声音暂未支持。").Size(12).Tertiary()
		}, layout.Gap(14), layout.Padding(insets(4, 0, 0, 0)))
	})
	switch {
	case accept:
		go m.o.Host.Accept()
	case reject || md.Cancelled() || !open:
		go m.o.Host.Reject()
	}
}

// controlledPanel is the bottom-right card shown while being controlled.
func (m *Main) controlledPanel(c *ui.Context, hs app.HostState) {
	th := c.Theme
	el := time.Since(hs.Since).Truncate(time.Second)
	c.WakeAt(time.Now().Add(time.Second))
	ui.Column(c, func() {
		ui.Row(c, func() {
			pulseDot(c, th.Error.Base, 8)
			ui.Text(c, hs.Peer+" 正在控制本机").Strong().Ellipsis().Layout(layout.Grow(1))
			mono(c, formatElapsed(el), 12, 400, th.TextTertiary)
		}, layout.Gap(8), layout.Middle())
		ui.Row(c, func() {
			for _, p := range perms {
				t := ui.Tag(c, p.label).Round()
				if p.on {
					t.Color("blue")
				} else {
					t.Disabled(true)
				}
			}
		}, layout.GapXY(6, 6), layout.WrapItems())
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

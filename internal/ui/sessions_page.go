package appui

import (
	"fmt"
	"log"
	"time"

	"github.com/tangoui/tango/ui"
	"github.com/tangoui/tango/ui/layout"
	"github.com/tangoui/tango/ui/text"

	"github.com/tangoui/kkdesk/internal/app"
	"github.com/tangoui/kkdesk/internal/config"
)

// sessionsPage is 远程会话: the sessions this machine started and the one
// controlling this machine.
func (m *Main) sessionsPage(c *ui.Context, hs app.HostState) {
	th := c.Theme
	out := app.Sessions()
	incoming := hs.Phase == app.HostActive
	ui.Title(c, "远程会话", 4)
	if len(out) == 0 && !incoming {
		panel(c, func() {
			ui.Empty(c).Title("没有进行中的会话").Description("在「连接」页输入对方地址和密码，或在「我的设备」里点「连接」。")
			ui.Row(c, func() {
				if ui.Button(c, "去连接").Primary().Clicked() {
					m.nav = "connect"
					c.Invalidate()
				}
			}, layout.JustifyContent(layout.JustifyCenter))
		}, layout.Pad(48), layout.Gap(16))
		return
	}
	c.WakeAt(time.Now().Add(time.Second)) // durations and stats

	sectionHead(c, "本机发起的会话", len(out))
	if len(out) == 0 {
		panel(c, func() { ui.Empty(c).Simple().Description("本机没有在控制其他电脑") }, layout.Pad(24))
	} else {
		cols := []ui.TableColumn{
			{Key: "addr", Title: "对方地址", MinWidth: 150, Render: func(i int) {
				mono(c, config.DisplayAddr(out[i].Addr), 13, text.Medium, th.Text)
			}},
			{Key: "mode", Title: "模式", Width: 90, Render: func(i int) {
				switch {
				case out[i].FilesOnly:
					ui.Tag(c, "传文件").Color("purple")
				case out[i].State.ViewOnly:
					ui.Tag(c, "仅查看").Color("blue")
				default:
					ui.Tag(c, "控制").Color("green")
				}
			}},
			{Key: "dur", Title: "持续时间", Width: 90, Text: func(i int) string {
				return formatElapsed(time.Since(out[i].Since).Truncate(time.Second))
			}},
			{Key: "lat", Title: "延迟", Width: 80, Align: ui.AlignRight, Render: func(i int) {
				st := out[i].State
				if out[i].FilesOnly || st.Phase != app.ViewerStreaming {
					ui.Text(c, "—").Tertiary()
					return
				}
				ms := int(st.Stats.LatencyP50.Milliseconds())
				mono(c, fmt.Sprintf("%d ms", ms), 13, text.Regular, latencyColor(th, ms))
			}},
			{Key: "fps", Title: "帧率", Width: 80, Align: ui.AlignRight, Text: func(i int) string {
				st := out[i].State
				if out[i].FilesOnly || st.Phase != app.ViewerStreaming {
					return "—"
				}
				return fmt.Sprintf("%.0f fps", st.Stats.FPS)
			}},
			{Key: "bps", Title: "码率", Width: 100, Align: ui.AlignRight, Text: func(i int) string {
				st := out[i].State
				if out[i].FilesOnly || st.Phase != app.ViewerStreaming {
					return "—"
				}
				return fmt.Sprintf("%.1f Mbps", st.Stats.BitrateBps/1e6)
			}},
			{Key: "actions", Title: "操作", Width: 330, Render: func(i int) { m.sessionActions(c, out[i]) }},
		}
		ui.Table(c, cols, len(out)).RowKey(func(i int) string { return fmt.Sprintf("%p", out[i].Viewer) }).
			NoHover().A11yLabel("本机发起的会话").Show()
	}

	sectionHead(c, "正在控制本机", boolCount(incoming))
	if !incoming {
		panel(c, func() { ui.Empty(c).Simple().Description("没有人在控制本机") }, layout.Pad(24))
		return
	}
	cols := []ui.TableColumn{
		{Key: "addr", Title: "对方地址", MinWidth: 150, Render: func(int) {
			mono(c, hs.Peer, 13, text.Medium, th.Text)
		}},
		{Key: "dur", Title: "时长", Width: 90, Text: func(int) string {
			return formatElapsed(time.Since(hs.Since).Truncate(time.Second))
		}},
		{Key: "perm", Title: "权限", MinWidth: 200, Render: func(int) {
			ui.Row(c, func() {
				for _, p := range perms {
					if !permShown(hs, p.label) {
						continue
					}
					on, _ := permState(hs, p.label, p.on)
					if on {
						ui.Tag(c, p.label).Color("blue")
					}
				}
			}, layout.Gap(4), layout.WrapItems())
		}},
		{Key: "actions", Title: "操作", Width: 100, Render: func(int) {
			if ui.Button(c, "断开").AutoInsertSpace(false).Danger().Small().Clicked() {
				go m.o.Host.Disconnect()
			}
		}},
	}
	ui.Table(c, cols, 1).NoHover().A11yLabel("正在控制本机").Show()
}

// sessionActions are the buttons of an outgoing session.
func (m *Main) sessionActions(c *ui.Context, s app.SessionInfo) {
	v := s.Viewer
	a := m.o.App
	ui.Row(c, func() {
		if !s.FilesOnly && ui.Button(c, "切到窗口").Small().Clicked() {
			a.Post(func() { showSession(v) })
		}
		if ui.Button(c, "文件传输").Small().Clicked() {
			a.Post(func() {
				if err := OpenFiles(a, v, false, nil); err != nil {
					log.Printf("kkdesk: file window: %v", err)
				}
			})
		}
		if !s.FilesOnly {
			label := "切换为仅查看"
			if s.State.ViewOnly {
				label = "切换为控制"
			}
			if ui.Button(c, label).Small().Clicked() {
				v.SetViewOnly(!s.State.ViewOnly)
			}
		}
		if ui.Button(c, "断开").AutoInsertSpace(false).Small().Danger().Clicked() {
			go v.Close()
		}
	}, layout.Gap(6), layout.Middle(), layout.WrapItems())
}

// sectionHead is a section title with a count.
func sectionHead(c *ui.Context, title string, n int) {
	ui.Row(c, func() {
		ui.Text(c, title).Strong()
		ui.Text(c, itoa(n)).Size(12).Tertiary()
	}, layout.Gap(8), layout.Middle(), layout.Padding(insets(4, 0, 0, 0)))
}

func boolCount(b bool) int {
	if b {
		return 1
	}
	return 0
}

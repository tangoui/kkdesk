package appui

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/tangoui/tango/ui"
	"github.com/tangoui/tango/ui/icon"
	"github.com/tangoui/tango/ui/icon/antd"
	"github.com/tangoui/tango/ui/layout"
	"github.com/tangoui/tango/ui/text"

	"github.com/tangoui/kkdesk/internal/app"
	"github.com/tangoui/kkdesk/internal/config"
)

// filesPageState is the 仅传文件 form of the 文件传输 page.
type filesPageState struct {
	addr, pw string
}

// filesPage is 文件传输: the connected sessions with a button for their file
// window, a files-only quick connect and the transfers of this run.
func (m *Main) filesPage(c *ui.Context) {
	th := c.Theme
	ui.Title(c, "文件传输", 4)
	m.connFeedback(c, "transfer")
	sessions := app.Sessions()
	ui.Row(c, func() {
		// Connected sessions.
		panel(c, func() {
			cardHead(c, "已连接的会话", "在会话的文件窗口里浏览两边的文件并传输。")
			if len(sessions) == 0 {
				ui.Empty(c).Simple().Description("没有已连接的会话")
			}
			for _, s := range sessions {
				ui.Key(c, fmt.Sprintf("%p", s.Viewer), func() {
					ui.Row(c, func() {
						ui.Row(c, func() {
							ui.Icon(c, antd.DesktopOutlined).Size(16).Color(th.Primary.Base)
						}, layout.Size(32, 32), layout.Center()).Bg(th.Primary.Bg).Radius(th.RadiusSM)
						ui.Column(c, func() {
							mono(c, config.DisplayAddr(s.Addr), 14, text.Medium, th.Text)
							mode := "远程控制"
							switch {
							case s.FilesOnly:
								mode = "传文件"
							case s.State.ViewOnly:
								mode = "仅查看"
							}
							ui.Text(c, mode+" · 已连接 "+formatElapsed(time.Since(s.Since).Truncate(time.Second))).Size(12).Tertiary().NoWrap()
						}, layout.Grow(1), layout.MinW(0), layout.Gap(2))
						if ui.Button(c, "打开文件传输").Icon(icon.Swap).Small().Clicked() {
							v, a := s.Viewer, m.o.App
							a.Post(func() {
								if err := OpenFiles(a, v, false, nil); err != nil {
									log.Printf("kkdesk: file window: %v", err)
								}
							})
						}
					}, layout.Gap(10), layout.Middle(), layout.PadVH(10, 12)).Bg(th.FillQuaternary).Radius(th.Radius)
				})
			}
		}, layout.Grow(1), layout.Basis(340), layout.Pad(20), layout.Gap(8))
		if len(sessions) > 0 {
			c.WakeAt(time.Now().Add(time.Second))
		}

		// 仅传文件 quick connect.
		m.mu.Lock()
		connecting := m.connecting
		m.mu.Unlock()
		panel(c, func() {
			cardHead(c, "仅传文件", "只连接对方的文件，不打开远程画面。")
			ui.Column(c, func() {
				ui.Text(c, "对方地址").Size(13).Secondary()
				ui.Input(c, &m.xfer.addr).Placeholder("例如 192.168.1.20").A11yLabel("传文件地址")
			}, layout.Gap(8))
			var submit bool
			ui.Column(c, func() {
				ui.Text(c, "密码").Size(13).Secondary()
				submit = ui.Input(c, &m.xfer.pw).Password().Placeholder("临时密码或固定密码").A11yLabel("传文件密码").Submitted()
			}, layout.Gap(8))
			addr := strings.TrimSpace(m.xfer.addr)
			b := ui.Button(c, "连接并传文件").Primary().Block().Loading(connecting).Disabled(addr == "")
			b.Layout(layout.Margin(insets(8, 0, 0, 0)))
			if (b.Clicked() || submit) && !connecting && addr != "" {
				m.connect(addr, strings.TrimSpace(m.xfer.pw), modeTransfer)
				m.xfer.pw = "" // not kept
			}
		}, layout.Grow(1), layout.Basis(300), layout.Pad(20), layout.Gap(8))
	}, layout.Gap(16), layout.WrapItems(), layout.AlignItems(layout.AlignStretch))

	m.transferHistory(c)
}

// transferHistory is the table of this run's transfers.
func (m *Main) transferHistory(c *ui.Context) {
	th := c.Theme
	hist := app.TransferHistory()
	ui.Row(c, func() {
		ui.Text(c, "传输记录").Strong()
		if len(hist) > 0 {
			ui.Text(c, itoa(len(hist))).Size(12).Tertiary()
		}
		ui.Text(c, "仅本次运行").Size(12).Tertiary()
		ui.Spacer(c)
		if len(hist) > 0 && ui.Button(c, "清空").Text().Small().Clicked() {
			app.ClearTransferHistory()
		}
	}, layout.Gap(8), layout.Middle(), layout.Padding(insets(4, 0, 0, 0)))
	if len(hist) == 0 {
		panel(c, func() { ui.Empty(c).Simple().Description("还没有传输记录") }, layout.Pad(24))
		return
	}
	cols := []ui.TableColumn{
		{Key: "at", Title: "时间", Width: 110, Text: func(i int) string { return hist[i].At.Format("15:04:05") }},
		{Key: "name", Title: "文件名", MinWidth: 160, Ellipsis: true, Text: func(i int) string {
			r := hist[i]
			if r.Files > 1 {
				return fmt.Sprintf("%s（%d 个文件）", r.Name, r.Files)
			}
			return r.Name
		}},
		{Key: "dir", Title: "方向", Width: 190, Render: func(i int) {
			r := hist[i]
			peer := config.DisplayAddr(r.Peer)
			if r.Upload {
				ui.Text(c, "↑ 上传到 "+peer).Size(13).Ellipsis()
			} else {
				ui.Text(c, "↓ 从 "+peer+" 下载").Size(13).Ellipsis()
			}
		}},
		{Key: "size", Title: "大小", Width: 90, Align: ui.AlignRight, Text: func(i int) string { return fmtBytes(hist[i].Bytes) }},
		{Key: "speed", Title: "平均速度", Width: 100, Align: ui.AlignRight, Text: func(i int) string {
			r := hist[i]
			if s := r.Elapsed.Seconds(); s > 0 && r.Bytes > 0 {
				return fmtBytes(int64(float64(r.Bytes)/s)) + "/s"
			}
			return "—"
		}},
		{Key: "result", Title: "结果", Width: 120, Render: func(i int) {
			r := hist[i]
			switch r.Result {
			case app.TransferDone:
				ui.Tag(c, "完成").Color("success")
			case app.TransferCanceled:
				ui.Tag(c, "已取消")
			default:
				ui.Row(c, func() {
					ui.Tag(c, "失败").Color("error")
					if r.Err != "" {
						ui.Tooltip(c, r.Err, func() { ui.Icon(c, antd.InfoCircleOutlined).Size(14).Color(th.TextTertiary) })
					}
				}, layout.Gap(4), layout.Middle())
			}
		}},
	}
	ui.Table(c, cols, len(hist)).NoHover().A11yLabel("传输记录").Show()
}

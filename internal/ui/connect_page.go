package appui

import (
	"fmt"
	"strings"
	"time"

	"github.com/tangoui/tango/ui"
	"github.com/tangoui/tango/ui/icon"
	"github.com/tangoui/tango/ui/icon/antd"
	"github.com/tangoui/tango/ui/layout"
	"github.com/tangoui/tango/ui/text"

	"github.com/tangoui/kkdesk/internal/app"
	"github.com/tangoui/kkdesk/internal/rendezvous"
)

var (
	modeOpts = []ui.Option{
		{Label: "远程控制", Value: modeControl},
		{Label: "仅查看", Value: modeView},
		{Label: "传文件", Value: modeTransfer},
	}
)

// connectPage is the 连接 page: the two cards and 最近连接.
func (m *Main) connectPage(c *ui.Context, hs app.HostState) {
	ui.Title(c, "连接", 4)
	ui.Row(c, func() {
		m.hostCard(c, hs)
		m.connectCard(c)
	}, layout.Gap(16), layout.WrapItems(), layout.AlignItems(layout.AlignStretch))
	m.recent(c)
}

// cardHead is the title and description of a connect card.
func cardHead(c *ui.Context, title, sub string) {
	ui.Column(c, func() {
		ui.Text(c, title).Size(16).Strong()
		if sub != "" {
			ui.Text(c, sub).Size(13).Tertiary()
		}
	}, layout.Gap(4), layout.Padding(insets(0, 0, 8, 0)))
}

// copyText writes s to the clipboard and confirms with a toast.
func copyText(c *ui.Context, s, msg string) {
	if w := c.Window(); w != nil {
		w.WriteClipboard(s)
	}
	ui.Message(c, "success", msg)
}

func (m *Main) hostCard(c *ui.Context, hs app.HostState) {
	th := c.Theme
	panel(c, func() {
		cardHead(c, "允许控制本机", "")
		// 本机地址
		ui.Column(c, func() {
			ui.Text(c, "本机地址（局域网用）").Size(12).Tertiary()
			if len(hs.Addrs) == 0 {
				ui.Text(c, "没有可用的网络地址").Secondary()
			}
			for i, a := range hs.Addrs {
				addr := fmt.Sprintf("%s:%d", a, hs.Port)
				size := float32(26)
				if i > 0 {
					size = 18
				}
				ui.Row(c, func() {
					mono(c, addr, size, text.Semibold, th.Text)
					ui.Spacer(c)
					if ui.IconButton(c, icon.Copy, "复制地址 "+addr).Size(28, 16).Clicked() {
						copyText(c, addr, "已复制地址")
					}
				}, layout.Gap(4), layout.Middle())
			}
		}, layout.Gap(4), layout.PadVH(12, 0))
		if hs.RemoteCode == "" && hs.DeviceCode != "" {
			ui.Text(c, "本机设备码 "+hs.DeviceCode).Size(12).Secondary()
		}
		m.remoteDeviceCode(c, hs)
		hline(c)
		// 临时密码
		ui.Column(c, func() {
			ui.Row(c, func() {
				ui.Text(c, "临时密码").Size(12).Tertiary()
				ui.Spacer(c)
				m.rotate = hs.Rotate
				if ui.Select(c, &m.rotate, rotateOpts).Size(ui.Small).Width(140).A11yLabel("密码更换频率").Changed() {
					m.setRotate(c, m.rotate)
				}
			}, layout.Middle())
			ui.Row(c, func() {
				pw := strings.Repeat("•", len(hs.Password))
				if m.showPW {
					pw = hs.Password
				}
				mono(c, pw, 22, text.Semibold, th.Text)
				ui.Spacer(c)
				eye, tip := icon.Eye, "显示密码"
				if m.showPW {
					eye, tip = icon.EyeInvisible, "隐藏密码"
				}
				if ui.IconButton(c, eye, tip).Size(28, 16).Clicked() {
					m.showPW = !m.showPW
					c.Invalidate()
				}
				if ui.IconButton(c, icon.Reload, "更换密码").Size(28, 16).Clicked() {
					go m.o.Host.RefreshPassword()
				}
				if ui.IconButton(c, icon.Copy, "复制密码").Size(28, 16).Clicked() {
					copyText(c, hs.Password, "已复制密码")
				}
			}, layout.Gap(4), layout.Middle())
		}, layout.Gap(4), layout.PadVH(12, 0))
		// 允许被远程控制: the switch shows what the host wants, also while it
		// retries a busy port.
		m.allow = hs.Enabled
		ui.Row(c, func() {
			col := th.Success.Base
			if !m.allow {
				col = th.TextQuaternary
			}
			dot(c, col, 6)
			ui.Text(c, "允许被远程控制").Size(13).Secondary().Layout(layout.Grow(1))
			if ui.Switch(c, &m.allow).A11yLabel("允许被远程控制").Changed() {
				_ = m.o.Host.SetEnabled(m.allow) // quick; a busy port is retried in the background
			}
		}, layout.Gap(10), layout.Middle(), layout.PadVH(10, 12)).Bg(th.FillQuaternary).Radius(th.Radius)
		m.permissions(c)
		if st := hostStatus(hs); st != "" {
			ui.Text(c, st).Size(12).Tertiary()
		}
		if hs.PasswordLockSeconds > 0 {
			ui.Alert(c, "warning", fmt.Sprintf("多次密码错误，已暂停接受密码连接 %d 秒", hs.PasswordLockSeconds)).ShowIcon(true).Show()
		}
		if hs.Err != "" {
			ui.Alert(c, "warning", hs.Err).ShowIcon(true).Show()
		}
		ui.Row(c, func() {
			if ui.Button(c, "复制并分享").Icon(icon.Copy).Disabled(len(hs.Addrs) == 0).Clicked() {
				copyText(c, shareText(hs), "已复制，可以直接发给对方")
			}
			if ui.Button(c, "手机扫码连接").Icon(antd.ScanOutlined).Disabled(!m.o.Host.PairingAvailable()).Clicked() {
				m.pairing.open = true
				c.Invalidate()
			}
		}, layout.Gap(8), layout.Middle(), layout.WrapItems(), layout.Padding(insets(4, 0, 0, 0)))
	}, layout.Grow(1), layout.Basis(340), layout.Pad(20), layout.Gap(8))
}

// hostStatus is the one-line state of the controlled side.
func hostStatus(hs app.HostState) string {
	switch hs.Phase {
	case app.HostListening:
		if hs.Notice != "" {
			return hs.Notice
		}
		return "等待连接…"
	case app.HostPending:
		if hs.FilesOnly {
			return hs.Peer + " 请求传输文件"
		}
		return hs.Peer + " 请求控制本机"
	case app.HostActive:
		if hs.FilesOnly {
			return hs.Peer + " 正在传输文件"
		}
		return "正在被 " + hs.Peer + " 控制"
	}
	return hs.Notice
}

// shareText is what 复制并分享 puts on the clipboard.
func shareText(hs app.HostState) string {
	var b strings.Builder
	b.WriteString("用 kkdesk 远程协助我：\n")
	for _, a := range hs.Addrs {
		fmt.Fprintf(&b, "局域网地址 %s:%d\n", a, hs.Port)
	}
	fmt.Fprintf(&b, "临时密码 %s", hs.Password)
	if hs.RemoteCode != "" {
		fmt.Fprintf(&b, "\n不在同一个网络时用跨网设备码 %s（填到「对方地址或设备码」里，密码同上）", hs.RemoteCode)
	}
	return b.String()
}

// connectTarget is what the 连接 button dials: a LAN address, or a device
// code (remote) for a cross-network connection, with the password, which
// is required for both.
func (f *Form) connectTarget() (addr, password string, remote, ok bool) {
	addr, password = strings.TrimSpace(f.PeerAddr), strings.TrimSpace(f.Password)
	if rendezvous.IsCode(addr) {
		addr, remote = rendezvous.NormalizeCode(addr), true
	}
	return addr, password, remote, addr != "" && password != ""
}

func (m *Main) connectCard(c *ui.Context) {
	m.mu.Lock()
	connecting, errMsg := m.connecting, m.connErr
	if m.connFrom != "connect" {
		errMsg = "" // shown on the page that started it
	}
	m.mu.Unlock()
	st := m.o.Form
	hasRemote := m.o.Store.Settings().RemoteURL != ""
	addr, pw, remote, ok := st.connectTarget()
	panel(c, func() {
		cardHead(c, "控制其他电脑", "")
		var submit bool
		ui.Column(c, func() {
			ui.Text(c, "对方地址或设备码").Size(13).Secondary()
			ui.Input(c, &st.PeerAddr).Size(ui.Large).Placeholder("例如 192.168.1.20 或 KD-ABCD2345EFGH").A11yLabel("对方地址")
			switch {
			case addr == "":
			case remote:
				ui.Text(c, "跨网连接：经跨网服务连接不在同一个网络的电脑。").Size(12).Tertiary()
			case strings.HasPrefix(strings.ToUpper(addr), "KD-"):
				ui.Text(c, "设备码以 KD- 开头，共 15 位，请检查是否抄错。").Size(12).Color(c.Theme.Error.Base)
			default:
				ui.Text(c, "局域网直连：两台电脑需要在同一个路由器或 Wi-Fi 下。").Size(12).Tertiary()
			}
		}, layout.Gap(6))
		if remote && !hasRemote {
			ui.Alert(c, "warning", "还没有配置跨网服务：请先在「设置 › 跨网连接」填写跨网服务地址和密钥（两台电脑填同一个）。").ShowIcon(true).Show()
			if ui.Button(c, "去设置").Small().Clicked() {
				m.nav, m.set.tab = "settings", "remote"
			}
		}
		ui.Column(c, func() {
			ui.Text(c, "临时密码").Size(13).Secondary()
			submit = ui.Input(c, &st.Password).Size(ui.Large).Password().Placeholder("对方的临时密码或固定密码").A11yLabel("临时密码").Submitted()
		}, layout.Gap(6), layout.Padding(insets(6, 0, 0, 0)))
		ui.Row(c, func() {
			ui.Text(c, "连接方式").Size(13).Tertiary()
			ui.Segmented(c, &st.Mode, modeOpts).A11yLabel("连接方式")
		}, layout.Gap(12), layout.Middle(), layout.WrapItems(), layout.Padding(insets(8, 0, 0, 0)))
		b := ui.Button(c, "连接").AutoInsertSpace(false).Primary().Large().Block().Loading(connecting).Disabled(!ok)
		b.Layout(layout.Margin(insets(8, 0, 0, 0)))
		if (b.Clicked() || submit) && !connecting && ok {
			m.connect(addr, pw, st.Mode)
		}
		if errMsg != "" {
			ui.Alert(c, "error", errMsg).ShowIcon(true).Show()
			// A LAN address the other computer cannot be reached at: it is
			// probably on another network.
			if !remote && strings.Contains(errMsg, "不在同一个局域网") {
				ui.Text(c, "不在同一个网络？请对方在连接页复制「本机跨网设备码」发给你，填到上面的输入框。").Size(12).Tertiary()
			}
		}
	}, layout.Grow(1), layout.Basis(340), layout.Pad(20), layout.Gap(8))
}

// recent is the 最近连接 section: real connections of this run only.
func (m *Main) recent(c *ui.Context) {
	th := c.Theme
	f := m.o.Form
	ui.Row(c, func() {
		ui.Text(c, "最近连接").Strong()
		if len(f.Recent) > 0 {
			ui.Text(c, itoa(len(f.Recent))).Size(12).Tertiary()
		}
		ui.Spacer(c)
		if len(f.Recent) > 0 && ui.Button(c, "清空记录").Text().Small().Clicked() {
			f.Recent = nil
			c.Invalidate()
		}
	}, layout.Gap(8), layout.Middle(), layout.WrapItems(), layout.Padding(insets(4, 0, 0, 0)))
	if len(f.Recent) == 0 {
		panel(c, func() {
			ui.Empty(c).Simple().Description("还没有连接记录")
		}, layout.Pad(24))
		return
	}
	ui.Row(c, func() {
		for _, r := range f.Recent {
			card := panel(c, func() {
				ui.Row(c, func() {
					ui.Icon(c, antd.DesktopOutlined).Size(28).Color(th.Primary.Base)
				}, layout.H(88), layout.Center()).Bg(th.Primary.Bg).Radius(th.RadiusSM)
				ui.Column(c, func() {
					mono(c, r.Addr, 14, text.Medium, th.Text)
					mode := "远程控制"
					switch {
					case r.Files:
						mode = "传文件"
					case r.ViewOnly:
						mode = "仅查看"
					}
					ui.Text(c, whenText(r.At)+" · "+mode).Size(12).Tertiary().NoWrap()
				}, layout.Gap(2))
			}, layout.W(200), layout.Pad(10), layout.Gap(10)).Label(r.Addr)
			if card.Clicked() {
				f.PeerAddr = r.Addr
				f.Mode = modeControl
				switch {
				case r.Files:
					f.Mode = modeTransfer
				case r.ViewOnly:
					f.Mode = modeView
				}
				c.Invalidate()
			}
			if card.Hovered() {
				card.Border(1, th.Primary.Hover)
			}
		}
	}, layout.Gap(12), layout.WrapItems())
}

// whenText formats a recent connection time.
func whenText(t time.Time) string {
	now := time.Now()
	if y, m, d := t.Date(); y == now.Year() && m == now.Month() && d == now.Day() {
		return "今天 " + t.Format("15:04")
	}
	return t.Format("1月2日 15:04")
}

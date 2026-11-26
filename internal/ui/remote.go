package appui

import (
	"strings"

	"github.com/tangoui/tango/ui"
	"github.com/tangoui/tango/ui/icon"
	"github.com/tangoui/tango/ui/layout"
	"github.com/tangoui/tango/ui/text"

	"github.com/tangoui/kkdesk/internal/app"
	"github.com/tangoui/kkdesk/internal/config"
	"github.com/tangoui/kkdesk/internal/rendezvous"
)

func (m *Main) remoteDeviceCode(c *ui.Context, hs app.HostState) {
	switch {
	case hs.RemoteCode != "":
		ui.Column(c, func() {
			ui.Text(c, "本机跨网设备码（不在同一个网络时用）").Size(12).Tertiary()
			ui.Row(c, func() {
				mono(c, hs.RemoteCode, 18, text.Semibold, c.Theme.Text)
				ui.Spacer(c)
				if ui.IconButton(c, icon.Copy, "复制设备码").Size(28, 16).Clicked() {
					copyText(c, hs.RemoteCode, "已复制设备码")
				}
			}, layout.Gap(8), layout.Middle())
		}, layout.Gap(4))
	case hs.RemoteErr != "":
		ui.Alert(c, "warning", hs.RemoteErr).ShowIcon(true).Show()
	case hs.Enabled && m.o.Store.Settings().RemoteURL != "":
		ui.Text(c, "正在连接跨网服务…").Size(12).Tertiary()
	case hs.Enabled:
		ui.Row(c, func() {
			ui.Text(c, "还没配置跨网连接：不在同一个网络时，别人连不上本机。").Size(12).Tertiary().Layout(layout.Grow(1), layout.MinW(0))
			if ui.Button(c, "配置跨网").Small().Clicked() {
				m.nav, m.set.tab = "settings", "remote"
			}
		}, layout.Gap(8), layout.Middle(), layout.WrapItems())
	}
}

// remoteSettings is 设置 › 跨网连接: the state of this computer on the
// cross-network service, then the service's address, public key and token.
func (m *Main) remoteSettings(c *ui.Context, f *ui.FormW, hs app.HostState) {
	s := &m.set
	f.Item("当前状态").Help("两台电脑不在同一个网络时，通过自己部署的跨网服务（kkdesk-server）用设备码连接。").Body(func() {
		controlRow(c, func() {
			th := c.Theme
			switch {
			case m.o.Store.Settings().RemoteURL == "":
				dot(c, th.TextQuaternary, 6)
				ui.Text(c, "未配置").Size(14)
			case hs.RemoteCode != "":
				dot(c, th.Success.Base, 6)
				ui.Text(c, "已连接，本机设备码").Size(14)
				mono(c, hs.RemoteCode, 14, text.Semibold, th.Text)
			case hs.RemoteErr != "":
				dot(c, th.Warning.Base, 6)
				ui.Text(c, hs.RemoteErr).Size(14).Ellipsis().Layout(layout.Grow(1), layout.MinW(0))
			case !hs.Enabled:
				dot(c, th.TextQuaternary, 6)
				ui.Text(c, "已配置；开启「允许被远程控制」后连接").Size(14)
			default:
				dot(c, th.Warning.Base, 6)
				ui.Text(c, "正在连接跨网服务…").Size(14)
			}
		}, layout.Gap(8))
	})
	helpGap(c)
	addr := f.Item("跨网服务")
	if s.remoteErr != "" {
		addr.Error(s.remoteErr)
	} else {
		addr.Help("两台电脑填同一个地址。")
	}
	addr.Body(func() {
		ui.Input(c, &s.remoteURL).Placeholder("https://desk.example.com 或 IP:端口").A11yLabel("跨网服务地址")
	})
	helpGap(c)
	f.Item("服务器公钥").Help("用 IP 地址时填写服务端安装后输出的公钥；用域名和正式证书时可以留空。").Body(func() {
		ui.Input(c, &s.remotePublicKey).Placeholder("IP 模式填服务端输出的公钥").A11yLabel("跨网服务器公钥")
	})
	helpGap(c)
	f.Item("服务密钥").Help("服务端的 KKDESK_SERVER_TOKEN，两台电脑填同一个。").Body(func() {
		ui.Row(c, func() {
			field := ui.Input(c, &s.remoteToken).Password().A11yLabel("跨网服务密钥")
			field.Layout(layout.Grow(1), layout.MinW(0))
			if ui.Button(c, "保存跨网配置").Clicked() || field.Submitted() {
				cfg := rendezvous.Config{
					URL: strings.TrimSpace(s.remoteURL), Token: strings.TrimSpace(s.remoteToken),
					PublicKey: strings.TrimSpace(s.remotePublicKey),
				}
				if err := cfg.Validate(); err != nil {
					s.remoteErr = err.Error()
					c.Invalidate()
					return
				}
				s.remoteErr = ""
				err := m.o.Store.UpdateSettings(func(st *config.Settings) {
					st.RemoteURL, st.RemoteToken = cfg.URL, cfg.Token
					st.RemotePublicKey = cfg.PublicKey
				})
				m.o.Host.SetRemote(cfg)
				if err != nil {
					ui.Message(c, "error", userErr(err))
				} else {
					ui.Message(c, "success", "已保存跨网配置")
				}
			}
		}, layout.Gap(8), layout.Middle())
	})
}

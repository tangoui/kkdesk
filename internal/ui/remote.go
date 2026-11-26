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
	}
}

func (m *Main) remoteSettings(c *ui.Context, f *ui.FormW) {
	s := &m.set
	addr := f.Item("跨网服务")
	if s.remoteErr != "" {
		addr.Error(s.remoteErr)
	}
	addr.Body(func() {
		ui.Input(c, &s.remoteURL).Placeholder("https://desk.example.com 或 IP:端口").A11yLabel("跨网服务地址")
	})
	f.Item("服务器公钥").Body(func() {
		ui.Input(c, &s.remotePublicKey).Placeholder("IP 模式填服务端输出的公钥").A11yLabel("跨网服务器公钥")
	})
	f.Item("服务密钥").Body(func() {
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

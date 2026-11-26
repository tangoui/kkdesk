package appui

import (
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"sync"

	"github.com/tangoui/tango/service/updater/updateui"
	"github.com/tangoui/tango/sys/shell"
	"github.com/tangoui/tango/ui"
	"github.com/tangoui/tango/ui/layout"
	"github.com/tangoui/tango/ui/text"
	"github.com/tangoui/tango/ui/theme"

	"github.com/tangoui/kkdesk/internal/app"
	"github.com/tangoui/kkdesk/internal/config"
)

// settingsState is the 设置 page: the selected group and the drafts of the
// fields that are applied with a button (fixed password, port).
type settingsState struct {
	tab                               string
	fixed                             string
	fixedErr                          string
	port                              float64
	portErr                           string
	saved                             int // st.Port the draft was taken from
	remoteURL, remoteToken, remoteErr string
	remotePublicKey                   string

	// bound controls, refreshed from the store every frame
	rotate, fps, bitrate, theme    string
	confirm, allow                 bool
	sessionClipboard, sessionFiles bool
	connectionMode                 string
	loginItem                      loginItemSetting
}

func (s *settingsState) init(st config.Settings) {
	s.tab = "security"
	s.port, s.saved = float64(st.Port), st.Port
	s.remoteURL, s.remoteToken = st.RemoteURL, st.RemoteToken
	s.remotePublicKey = st.RemotePublicKey
}

var settingsNav = []ui.MenuItem{
	{Key: "security", Label: "安全"},
	{Key: "quality", Label: "画质"},
	{Key: "network", Label: "局域网"},
	{Key: "remote", Label: "跨网连接"},
	{Key: "general", Label: "通用"},
}

var (
	rotateOpts = []ui.Option{
		{Label: "每次连接后更换", Value: config.RotateAfterSession},
		{Label: "每 12 小时更换", Value: config.Rotate12h},
		{Label: "每 24 小时更换", Value: config.Rotate24h},
		{Label: "不自动更换", Value: config.RotateNever},
	}
	themeOpts = []ui.Option{
		{Label: "跟随系统", Value: config.ThemeSystem},
		{Label: "浅色", Value: config.ThemeLight},
		{Label: "深色", Value: config.ThemeDark},
	}
)

func fpsOpts() []ui.Option {
	var o []ui.Option
	for _, f := range config.FrameRates {
		o = append(o, ui.Option{Label: fmt.Sprintf("%d fps", f), Value: itoa(f)})
	}
	return o
}

func bitrateOpts() []ui.Option {
	var o []ui.Option
	for _, b := range config.Bitrates {
		l := fmt.Sprintf("%d Mbps", b)
		if b == 0 {
			l = "自动"
		}
		o = append(o, ui.Option{Label: l, Value: itoa(b)})
	}
	return o
}

// update changes the settings and reports a failed save.
func (m *Main) update(c *ui.Context, fn func(*config.Settings)) {
	if err := m.o.Store.UpdateSettings(fn); err != nil {
		ui.Message(c, "error", userErr(err))
	}
}

// setRotate is the temporary password policy (连接 page and 设置 › 安全).
func (m *Main) setRotate(c *ui.Context, p string) {
	m.rotate = p
	m.update(c, func(s *config.Settings) { s.Rotate = p })
	m.o.Host.SetRotate(p)
}

// settingsPage is 设置: groups on the left, a form on the right. Every
// change applies at once and is saved.
func (m *Main) settingsPage(c *ui.Context, hs app.HostState) {
	s := &m.set
	st := m.o.Store.Settings()
	s.rotate, s.confirm, s.allow = hs.Rotate, st.Confirm, st.AllowRemote
	s.fps, s.bitrate, s.theme = itoa(st.FPS), itoa(st.BitrateMbps), st.Theme
	s.sessionClipboard, s.sessionFiles = st.AllowSessionClipboard, st.AllowSessionFiles
	s.connectionMode = st.DefaultConnectionMode
	if st.Port != s.saved { // changed elsewhere (the host moved off a reserved port)
		s.port, s.saved = float64(st.Port), st.Port
	}
	ui.Title(c, "设置", 4)
	ui.Row(c, func() {
		panel(c, func() {
			ui.Menu(c, settingsNav, &s.tab).Show()
		}, layout.W(160), layout.Padding(insets(4, 0, 4, 0)))
		panel(c, func() {
			ui.ConfigProvider(c).Theme(settingsTheme(c.Theme.Dark)).Body(func() {
				f := ui.Form(c).LabelWidth(140).LabelAlign(ui.AlignStart)
				f.Body(func() {
					switch s.tab {
					case "quality":
						m.qualitySettings(c, f)
					case "network":
						m.networkSettings(c, f, hs)
					case "remote":
						m.remoteSettings(c, f, hs)
					case "general":
						m.generalSettings(c, f)
					default:
						m.securitySettings(c, f, hs)
					}
				})
			})
		}, layout.Grow(1), layout.MinW(0), layout.Padding(insets(24, 24, 24, 24)))
	}, layout.Gap(16), layout.AlignItems(layout.AlignStart))
}

func (m *Main) securitySettings(c *ui.Context, f *ui.FormW, hs app.HostState) {
	s := &m.set
	f.Item("临时密码更换").Help("连接页的同名选项是同一个设置。").Body(func() {
		if ui.Select(c, &s.rotate, rotateOpts).Width(200).A11yLabel("临时密码更换频率").Changed() {
			m.setRotate(c, s.rotate)
		}
	})
	helpGap(c)
	it := f.Item("固定密码")
	switch {
	case s.fixedErr != "":
		it.Error(s.fixedErr)
	case hs.HasFixed:
		it.Help("已设置。临时密码和固定密码都可以用来连接本机。")
	default:
		it.Help(fmt.Sprintf("%d–%d 位。设置后，临时密码和固定密码都可以用来连接本机。", config.FixedMin, config.FixedMax))
	}
	it.Body(func() {
		ui.Row(c, func() {
			ph := "未设置"
			if hs.HasFixed {
				ph = "输入新的固定密码以替换"
			}
			submit := ui.Input(c, &s.fixed).Password().Placeholder(ph).Width(220).A11yLabel("固定密码").Submitted()
			if (ui.Button(c, "保存").Disabled(s.fixed == "").Clicked() || submit) && s.fixed != "" {
				if err := config.ValidFixedPassword(s.fixed); err != nil {
					s.fixedErr = err.Error()
				} else {
					pw := s.fixed
					m.update(c, func(st *config.Settings) { st.FixedPassword = pw })
					m.o.Host.SetFixedPassword(pw)
					s.fixed, s.fixedErr = "", ""
					ui.Message(c, "success", "固定密码已保存")
				}
				c.Invalidate()
			}
			if hs.HasFixed && ui.Button(c, "清除").Danger().Clicked() {
				m.update(c, func(st *config.Settings) { st.FixedPassword = "" })
				m.o.Host.SetFixedPassword("")
				s.fixed, s.fixedErr = "", ""
				ui.Message(c, "success", "固定密码已清除")
				c.Invalidate()
			}
		}, layout.Gap(8), layout.Middle(), layout.WrapItems())
	})
	helpGap(c)
	f.Item("连接前需要确认").Help("关闭后，填对密码的连接（局域网，或带密码的跨网连接）会被自动接受。").Body(func() {
		switchRow(c, &s.confirm, "别人连接时弹窗，由我决定是否接受", func(on bool) {
			m.update(c, func(st *config.Settings) { st.Confirm = on })
			m.o.Host.SetAutoAccept(!on)
		})
	})
	helpGap(c)
	f.Item("允许被远程控制").Help("启动时是否开启被控；连接页的开关只改变本次运行。").Body(func() {
		ui.Column(c, func() {
			switchRow(c, &s.allow, "启动后允许其他电脑连接本机", func(on bool) {
				m.update(c, func(st *config.Settings) { st.AllowRemote = on })
				_ = m.o.Host.SetEnabled(on)
			})
			m.permissions(c)
		}, layout.Gap(8))
	})
	helpGap(c)
	f.Item("剪贴板同步").Help("同步纯文本。可以在连接确认或会话中为本次连接单独调整。").Body(func() {
		switchRow(c, &s.sessionClipboard, "新会话默认允许剪贴板同步", func(on bool) {
			m.update(c, func(st *config.Settings) { st.AllowSessionClipboard = on })
			m.o.Host.SetSessionDefaults(on, s.sessionFiles)
		})
	})
	helpGap(c)
	f.Item("文件传输").Help("允许对方浏览和传输本机文件。新会话开始时应用。").Body(func() {
		switchRow(c, &s.sessionFiles, "新会话默认允许文件传输", func(on bool) {
			m.update(c, func(st *config.Settings) { st.AllowSessionFiles = on })
			m.o.Host.SetSessionDefaults(s.sessionClipboard, on)
		})
	})
	helpGap(c)
	f.Item("已配对手机").Body(func() { m.pairedPhones(c) })
}

// switchRow is a switch with its text; changed runs on a toggle.
func switchRow(c *ui.Context, val *bool, label string, changed func(bool)) {
	controlRow(c, func() {
		if ui.Switch(c, val).A11yLabel(label).Changed() {
			changed(*val)
		}
		ui.Text(c, label).Size(14)
	}, layout.Gap(8))
}

// controlRow lays out a form control shorter than the label line (a switch
// is 22 px, the label line 32 px) in a row as tall as the label line and
// centered in it, so the label and the control share a middle line instead
// of the label sitting lower.
func controlRow(c *ui.Context, fn func(), opts ...layout.Option) {
	formTheme := c.ThemeFor(theme.CompForm).Components.Form
	ui.Row(c, fn, append([]layout.Option{layout.H(formTheme.LabelHeight), layout.Middle()}, opts...)...)
}

func (m *Main) qualitySettings(c *ui.Context, f *ui.FormW) {
	s := &m.set
	f.Item("默认帧率").Help("新会话使用；会话中可在「控制中心」临时修改。").Body(func() {
		if ui.Segmented(c, &s.fps, fpsOpts()).A11yLabel("默认帧率").Changed() {
			fps, _ := strconv.Atoi(s.fps)
			m.update(c, func(st *config.Settings) { st.FPS = fps })
			m.o.Host.SetQuality(fps, m.o.Store.Settings().BitrateMbps)
		}
	})
	helpGap(c)
	f.Item("默认码率").Help("自动目前固定为 8 Mbps。").Body(func() {
		if ui.Select(c, &s.bitrate, bitrateOpts()).Width(200).A11yLabel("默认码率").Changed() {
			b, _ := strconv.Atoi(s.bitrate)
			m.update(c, func(st *config.Settings) { st.BitrateMbps = b })
			m.o.Host.SetQuality(m.o.Store.Settings().FPS, b)
		}
	})
}

func (m *Main) networkSettings(c *ui.Context, f *ui.FormW, hs app.HostState) {
	s := &m.set
	th := c.Theme
	m.mu.Lock() // portErr is also set by a failed listen in the background
	perr := s.portErr
	m.mu.Unlock()
	it := f.Item("监听端口")
	if perr != "" {
		it.Error(perr)
	} else {
		it.Help(fmt.Sprintf("默认 %d。修改后立即在新端口监听，进行中的会话不受影响。", config.DefaultPort))
	}
	it.Body(func() {
		ui.Row(c, func() {
			in := ui.InputNumber(c, &s.port).Min(1024).Max(65535).Precision(0).Width(140).A11yLabel("监听端口")
			submit := in.Submitted()
			if ui.Button(c, "应用").Disabled(int(s.port) == hs.Port).Clicked() || submit {
				m.applyPort(c, int(s.port))
			}
			ui.Text(c, "当前 "+itoa(hs.Port)).Size(12).Tertiary()
		}, layout.Gap(8), layout.Middle())
	})
	helpGap(c)
	f.Item("本机地址").Help("把地址和端口告诉对方即可连接。").Body(func() {
		ui.Column(c, func() {
			if len(hs.Addrs) == 0 {
				ui.Text(c, "没有可用的网络地址").Secondary()
			}
			for _, a := range hs.Addrs {
				mono(c, fmt.Sprintf("%s:%d", a, hs.Port), 14, text.Regular, th.Text)
			}
		}, layout.Gap(4), layout.Padding(insets(5, 0, 0, 0)))
	})
}

// applyPort saves the port and restarts the listener on it.
func (m *Main) applyPort(c *ui.Context, port int) {
	s := &m.set
	m.mu.Lock()
	defer m.mu.Unlock()
	if port < 1024 || port > 65535 {
		s.portErr = "端口范围 1024–65535"
		return
	}
	s.portErr = ""
	if err := m.o.Store.UpdateSettings(func(st *config.Settings) { st.Port = port }); err != nil {
		ui.Message(c, "error", userErr(err))
	}
	addr := ":" + itoa(port)
	go func() {
		if err := m.o.Host.SetAddr(addr); err != nil {
			m.mu.Lock()
			m.set.portErr = "无法监听端口 " + itoa(port) + "：" + err.Error()
			m.mu.Unlock()
			m.Invalidate()
		}
	}()
	ui.Message(c, "success", "已改为端口 "+itoa(port))
}

func (m *Main) generalSettings(c *ui.Context, f *ui.FormW) {
	s := &m.set
	f.Item("主题").Body(func() {
		if ui.Segmented(c, &s.theme, themeOpts).A11yLabel("主题").Changed() {
			th := s.theme
			m.update(c, func(st *config.Settings) { st.Theme = th })
			switchTheme(m.o.App, m.runner.Load(), th)
		}
	})
	f.Item("默认连接方式").Body(func() {
		if ui.Segmented(c, &s.connectionMode, modeOpts).A11yLabel("默认连接方式").Changed() {
			mode := s.connectionMode
			m.update(c, func(st *config.Settings) { st.DefaultConnectionMode = mode })
			m.o.Form.Mode = mode
		}
	})
	m.loginItemSettings(c, f)
	helpGap(c)
	f.Item("版本").Body(func() {
		formTheme := c.ThemeFor(theme.CompForm).Components.Form
		ui.Row(c, func() {
			ui.Text(c, "kkdesk "+app.Version).Size(formTheme.LabelFontSize)
			if u := m.o.Updater; u != nil && ui.Button(c, "检查更新").Small().Clicked() {
				a := m.o.App
				a.Post(func() { updateui.CheckNow(a, u, updateui.Options{}) })
			}
		}, layout.H(formTheme.LabelHeight), layout.Middle(), layout.Gap(12))
	})
	reveal := "在访达中显示"
	if runtime.GOOS == "windows" {
		reveal = "在资源管理器中显示"
	}
	f.Item("配置文件").Help("设置和我的设备都保存在这个文件里，不会上传。").Body(func() {
		controlRow(c, func() {
			ui.Text(c, m.o.Store.Path()).Size(13).Secondary().Ellipsis().Layout(layout.Grow(1), layout.MinW(0))
			if ui.Button(c, reveal).Small().Clicked() {
				path, a := m.o.Store.Path(), m.o.App
				a.Post(func() {
					if err := shell.RevealPath(a, path); err != nil {
						dir := path[:max(0, strings.LastIndexAny(path, `/\`))]
						_ = shell.RevealPath(a, dir) // the file is created by the first change
					}
				})
			}
		}, layout.Gap(8))
	})
}

// helpGap separates an item with a help line from the next one. Tango's form
// (like Ant's) lets the help line take the item's bottom margin, which
// leaves the next label right under the help text.
func helpGap(c *ui.Context) { ui.Div(c, layout.H(12)) }

// settingsHelpFontSize is the size of the grey help lines under the settings
// (Ant's FontSizeSM): the form's default, the body size, is too loud for
// the many explanations on this page.
const settingsHelpFontSize = 12

var (
	settingsThemeOnce [2]sync.Once
	settingsThemes    [2]*theme.Theme
)

// settingsTheme is the window's Ant Design theme with smaller form help
// lines, built once per light/dark.
func settingsTheme(dark bool) *theme.Theme {
	i := 0
	if dark {
		i = 1
	}
	settingsThemeOnce[i].Do(func() {
		opts := []theme.Option{theme.WithComponents(theme.Components{Form: theme.FormToken{MessageFontSize: settingsHelpFontSize}})}
		if dark {
			opts = append(opts, theme.Dark())
		}
		settingsThemes[i] = theme.AntDesign(opts...)
	})
	return settingsThemes[i]
}

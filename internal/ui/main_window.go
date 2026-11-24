// Package appui is mmdesk's interface (directory internal/ui): the main
// window (sidebar navigation; the 连接 page with this machine's address
// and password, the form to control another machine and the recent
// connections; the incoming-connection confirmation and the
// controlled-side panel) and the session window (tab strip with stats and
// 控制中心, the remote screen as a native video view). Layout and wording
// follow the prototype outputs/remote-desktop-ui/index.html.
package appui

import (
	"context"
	"io"
	"log"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tangoui/tango"
	"github.com/tangoui/tango/ui"
	"github.com/tangoui/tango/ui/icon"
	"github.com/tangoui/tango/ui/icon/antd"
	"github.com/tangoui/tango/ui/layout"
	"github.com/tangoui/tango/ui/text"
	"github.com/tangoui/tango/ui/theme"
	"github.com/tangoui/tango/uihost"

	"mmdesk/internal/app"
)

// Connection modes of the 控制其他电脑 form.
const (
	modeControl  = "control"
	modeView     = "view"
	modeTransfer = "transfer" // not implemented yet (disabled)
)

// Form is the persisted content of the 控制其他电脑 form.
type Form struct {
	PeerAddr string
	Password string
	Mode     string   // modeControl (default) or modeView
	Recent   []Recent // successful connections, newest first
}

// Recent is one entry of 最近连接.
type Recent struct {
	Addr     string
	At       time.Time
	ViewOnly bool
}

// maxRecent bounds the 最近连接 list.
const maxRecent = 8

// MainOptions configures the main window.
type MainOptions struct {
	App       *tango.App
	Host      *app.Host
	Form      *Form     // persisted across hot reloads
	ViewerLog io.Writer // stats of viewer sessions started from the form
	ViewOnly  bool      // force view-only for those sessions
	Hidden    bool      // create the window hidden (-connect)
	Error     string    // initial error shown on the form
}

// Main is the main window.
type Main struct {
	o       MainOptions
	runner  atomic.Pointer[ui.Runner]
	surface atomic.Pointer[tango.Surface]

	// UI thread
	allow    bool
	nav      string // selected sidebar item
	showPW   bool
	rotate   string // 临时密码 rotation (display only for now)
	hostname string

	mu         sync.Mutex
	connecting bool
	connErr    string
}

// Sidebar items.
var navItems = []ui.MenuItem{
	{Key: "connect", Label: "连接", Icon: icon.Link},
	{Key: "devices", Label: "我的设备", Icon: antd.LaptopOutlined},
	{Key: "sessions", Label: "远程会话", Icon: icon.Monitor},
	{Key: "transfer", Label: "文件传输", Icon: icon.Swap},
	{Key: "settings", Label: "设置", Icon: icon.Setting},
}

// OpenMain opens the main window. Call on the main thread.
func OpenMain(o MainOptions) (*Main, error) {
	host, _ := os.Hostname()
	m := &Main{o: o, allow: o.Host.State().Phase != app.HostOff, connErr: o.Error,
		nav: "connect", rotate: "每次连接后更换", hostname: host}
	if o.Form.Mode == "" {
		o.Form.Mode = modeControl
	}
	r, s, err := uihost.Open(o.App, uihost.Options{Surface: tango.SurfaceOptions{
		Title:     "mmdesk",
		Width:     1080,
		Height:    720,
		MinWidth:  720,
		MinHeight: 520,
		Resizable: true,
		Centered:  true,
		Hidden:    o.Hidden,
	}}, m.page, ui.RunnerOptions{Theme: theme.AntDesign()})
	if err != nil {
		return nil, err
	}
	m.runner.Store(r)
	m.surface.Store(s)
	return m, nil
}

// Invalidate redraws the window; safe from any goroutine.
func (m *Main) Invalidate() {
	if r := m.runner.Load(); r != nil {
		r.Invalidate()
	}
}

// ShowError shows msg on the form and brings the window up. Main thread.
func (m *Main) ShowError(msg string) {
	m.mu.Lock()
	m.connErr = msg
	m.mu.Unlock()
	if s := m.surface.Load(); s != nil {
		s.Show()
	}
	m.Invalidate()
}

func (m *Main) page(c *ui.Context) {
	th := c.Theme
	hs := m.o.Host.State()
	ui.Row(c, func() {
		m.sidebar(c, hs)
		ui.Column(c, func() {
			ui.Scroll(c, func() {
				ui.Column(c, func() {
					switch m.nav {
					case "connect":
						m.connectPage(c, hs)
					default:
						m.comingSoon(c)
					}
				}, layout.Gap(16), layout.Padding(insets(20, 24, 24, 24)))
			}, layout.Grow(1))
			if hs.Phase == app.HostActive {
				m.controlledPanel(c, hs)
			}
		}, layout.Grow(1)).Bg(th.BgLayout)
	}, layout.Grow(1))
	m.confirm(c, hs)
}

// sidebar is the left column: brand, navigation and status footer.
func (m *Main) sidebar(c *ui.Context, hs app.HostState) {
	th := c.Theme
	ui.Column(c, func() {
		ui.Row(c, func() {
			ui.Row(c, func() {
				ui.Icon(c, icon.Monitor).Size(14).Color(th.TextOnPrimary)
			}, layout.Size(24, 24), layout.Center()).Bg(th.Primary.Base).Radius(6)
			ui.Text(c, "mmdesk").Strong().NoWrap()
		}, layout.Gap(8), layout.Middle(), layout.PadVH(4, 16))
		ui.Menu(c, navItems, &m.nav).Show()
		ui.Spacer(c)
		ui.Column(c, func() {
			ui.Row(c, func() {
				name := m.hostname
				if name == "" {
					name = "本机"
				}
				ui.Avatar(c, string([]rune(name)[:1])).Size(32)
				ui.Column(c, func() {
					ui.Text(c, name).Size(13).Weight(text.Weight(500)).Ellipsis()
					ui.Text(c, "本机 · 局域网直连").Size(12).Tertiary().NoWrap()
				}, layout.Grow(1), layout.MinW(0))
			}, layout.Gap(10), layout.Middle())
			ui.Row(c, func() {
				col, msg, sub := th.Success.Base, "服务正常", "端口 "+itoa(hs.Port)
				switch {
				case hs.Phase == app.HostOff:
					col, msg, sub = th.TextQuaternary, "未开启被控", ""
				case hs.Err != "":
					col, msg, sub = th.Warning.Base, "需要处理", ""
				case hs.Phase == app.HostActive:
					col, msg, sub = th.Error.Base, "正在被控制", ""
				}
				dot(c, col, 6)
				ui.Text(c, msg).Size(12).Secondary().NoWrap()
				if sub != "" {
					ui.Text(c, "· "+sub).Size(12).Tertiary().NoWrap()
				}
			}, layout.Gap(6), layout.Middle())
		}, layout.Gap(10), layout.Padding(insets(12, 16, 16, 16))).Paint(topLine(th.Split))
	}, layout.W(200), layout.PadVH(12, 0)).Bg(th.BgContainer).Paint(endLine(th.Split))
}

// comingSoon is the placeholder of pages that are not built yet.
func (m *Main) comingSoon(c *ui.Context) {
	title := ""
	for _, it := range navItems {
		if it.Key == m.nav {
			title = it.Label
		}
	}
	ui.Title(c, title, 4)
	panel(c, func() {
		ui.Empty(c).Title("即将推出").Description("这个功能还在开发中，当前版本先使用「连接」。")
		ui.Row(c, func() {
			if ui.Button(c, "返回连接").Primary().Clicked() {
				m.nav = "connect"
				c.Invalidate()
			}
		}, layout.JustifyContent(layout.JustifyCenter))
	}, layout.Pad(48), layout.Gap(16))
}

// connect dials in the background and opens the session window.
func (m *Main) connect(addr, pw string, viewOnly bool) {
	m.mu.Lock()
	m.connecting, m.connErr = true, ""
	m.mu.Unlock()
	viewOnly = viewOnly || m.o.ViewOnly
	v := app.NewViewer(app.ViewerConfig{Addr: addr, Password: pw, ViewOnly: viewOnly, Log: m.o.ViewerLog}, nil)
	go func() {
		err := v.Connect(context.Background())
		m.mu.Lock()
		m.connecting = false
		if err != nil {
			m.connErr = err.Error()
		}
		m.mu.Unlock()
		m.Invalidate()
		if err != nil {
			return
		}
		m.o.App.Post(func() {
			m.addRecent(Recent{Addr: addr, At: time.Now(), ViewOnly: viewOnly})
			if err := OpenSession(m.o.App, v, func(msg string) {
				if msg != "" {
					m.ShowError(msg)
				}
			}); err != nil {
				log.Printf("mmdesk: session window: %v", err)
				v.Close()
			}
			m.Invalidate()
		})
	}()
}

// addRecent records a successful connection. Main thread.
func (m *Main) addRecent(r Recent) {
	f := m.o.Form
	out := []Recent{r}
	for _, e := range f.Recent {
		if e.Addr != r.Addr && len(out) < maxRecent {
			out = append(out, e)
		}
	}
	f.Recent = out
}

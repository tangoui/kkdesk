// Package appui is kkdesk's interface (directory internal/ui): the main
// window (sidebar navigation; the 连接 page with this machine's address
// and password, the form to control another machine and the recent
// connections; 我的设备, 远程会话, 文件传输 and 设置; the
// incoming-connection confirmation and the controlled-side panel) and the
// session window (tab strip with stats and
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
	"github.com/tangoui/tango/service/updater"
	"github.com/tangoui/tango/ui"
	"github.com/tangoui/tango/ui/icon"
	"github.com/tangoui/tango/ui/icon/antd"
	"github.com/tangoui/tango/ui/layout"
	"github.com/tangoui/tango/ui/text"
	"github.com/tangoui/tango/uihost"

	"github.com/tangoui/kkdesk/internal/app"
	"github.com/tangoui/kkdesk/internal/clipsync"
	"github.com/tangoui/kkdesk/internal/config"
	"github.com/tangoui/kkdesk/internal/rendezvous"
)

// Connection modes of the 控制其他电脑 form.
const (
	modeControl  = config.ConnectionControl
	modeView     = config.ConnectionView
	modeTransfer = config.ConnectionTransfer
)

// ModeTransfer is the 传文件 connection mode (Form.Mode).
const ModeTransfer = modeTransfer

// Form is the persisted content of the 控制其他电脑 form.
type Form struct {
	PeerAddr string
	Password string
	Mode     string   // modeControl (default), modeView or modeTransfer
	Recent   []Recent // successful connections, newest first

	ScreenRecordingPromptShown bool // suppress repeated prompts on hot reload
}

// Recent is one entry of 最近连接.
type Recent struct {
	Addr     string
	At       time.Time
	ViewOnly bool
	Files    bool // 传文件
}

// maxRecent bounds the 最近连接 list.
const maxRecent = 8

// MainOptions configures the main window.
type MainOptions struct {
	App       *tango.App
	Host      *app.Host
	Store     *config.Store    // settings and 我的设备
	Form      *Form            // persisted across hot reloads
	ViewerLog io.Writer        // stats of viewer sessions started from the form
	ViewOnly  bool             // force view-only for those sessions
	Hidden    bool             // create the window hidden (-connect)
	Error     string           // initial error shown on the form
	Clipboard *Clipboard       // synced with the other side while controlling
	Updater   *updater.Updater // self-update (设置 › 通用 › 检查更新); nil in development builds
}

// Main is the main window.
type Main struct {
	o       MainOptions
	runner  atomic.Pointer[ui.Runner]
	surface atomic.Pointer[tango.Surface]

	prober *app.Prober // online state of 我的设备

	// UI thread
	allow               bool
	nav                 string // selected sidebar item
	showPW              bool
	rotate              string // 临时密码 rotation (config.Rotate*), bound to the setting
	hostname            string
	dev                 devicesState
	xfer                filesPageState
	set                 settingsState
	screenRecording     screenRecordingGuide
	pairing             pairingUI
	pairingCancellation uint64

	mu         sync.Mutex
	connecting bool
	connErr    string
	connFrom   string // page that started the connection
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
	st := o.Store.Settings()
	m := &Main{o: o, allow: o.Host.State().Phase != app.HostOff, connErr: o.Error, connFrom: "connect",
		nav: "connect", rotate: o.Host.State().Rotate, hostname: host}
	m.set.init(st)
	if o.Form.Mode == "" {
		o.Form.Mode = st.DefaultConnectionMode
	}
	setThemeSetting(st.Theme)
	r, s, err := uihost.Open(o.App, uihost.Options{Theme: themeMode(), Surface: tango.SurfaceOptions{
		Title:     "kkdesk",
		Width:     1080,
		Height:    720,
		MinWidth:  720,
		MinHeight: 520,
		Resizable: true,
		Centered:  true,
		Hidden:    o.Hidden,
	}}, m.page, ui.RunnerOptions{})
	if err != nil {
		return nil, err
	}
	m.runner.Store(r)
	m.surface.Store(s)
	m.prober = app.NewProber(app.ProberConfig{}, m.Invalidate)
	m.syncProbeTargets()
	app.Watch(m.Invalidate)
	s.OnFocused(func() {
		r.Post(func() {
			m.set.loginItem.loaded = false
			if m.screenRecording.enabled {
				m.screenRecording.refresh(screenRecordingMissing())
			}
		})
	})
	s.OnClosed(func() { go m.prober.Close() }) // probing stops with the window
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
	if hs.PairingCancellation != m.pairingCancellation {
		m.pairingCancellation = hs.PairingCancellation
		ui.Message(c, "info", "手机已取消")
	}
	m.screenRecording.update(hs.Enabled, &m.o.Form.ScreenRecordingPromptShown, screenRecordingMissing)
	ui.Row(c, func() {
		m.sidebar(c, hs)
		ui.Column(c, func() {
			ui.Scroll(c, func() {
				ui.Column(c, func() {
					if msg := m.o.Store.Err(); msg != "" {
						ui.Alert(c, "warning", msg).ShowIcon(true).Show()
					}
					switch m.nav {
					case "devices":
						m.devicesPage(c)
					case "sessions":
						m.sessionsPage(c, hs)
					case "transfer":
						m.filesPage(c)
					case "settings":
						m.settingsPage(c, hs)
					default:
						m.connectPage(c, hs)
					}
				}, layout.Gap(16), layout.Padding(insets(20, 24, 24, 24)))
			}, layout.Grow(1))
			if hs.Phase == app.HostActive {
				m.controlledPanel(c, hs)
			}
		}, layout.Grow(1)).Bg(th.BgLayout)
	}, layout.Grow(1))
	if request := m.o.Host.PairingRequest(); request != nil {
		m.pairingConfirm(c, request)
	} else if hs.Phase == app.HostPending {
		m.confirm(c, hs)
	} else if m.pairing.open {
		m.pairingQR(c)
	} else {
		m.phoneSettingsDialog(c)
		m.screenRecording.dialog(c, os.Getenv("TANGO_DEV_ROLE") == "worker", openScreenRecordingSettings)
	}
}

// sidebar is the left column: brand, navigation and status footer.
func (m *Main) sidebar(c *ui.Context, hs app.HostState) {
	th := c.Theme
	ui.Column(c, func() {
		ui.Row(c, func() {
			if mark := brandMark(); mark != nil {
				ui.Image(c, mark).Size(24, 24) // its own blue plate: fine on light and dark
			} else {
				ui.Row(c, func() {
					ui.Icon(c, icon.Monitor).Size(14).Color(th.TextOnPrimary)
				}, layout.Size(24, 24), layout.Center()).Bg(th.Primary.Base).Radius(6)
			}
			ui.Text(c, "kkdesk").Strong().NoWrap()
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
					connection := "本机 · 局域网直连"
					if hs.RemoteCode != "" {
						connection = "本机 · 跨网服务已连接"
					}
					ui.Text(c, connection).Size(12).Tertiary().NoWrap()
				}, layout.Grow(1), layout.MinW(0))
			}, layout.Gap(10), layout.Middle())
			ui.Row(c, func() {
				col, msg, sub := th.Success.Base, "服务正常", "端口 "+itoa(hs.Port)
				switch {
				case hs.Err != "":
					// Wanted on but not listening (a refused or busy port):
					// not "off", something needs attention.
					col, msg, sub = th.Warning.Base, "需要处理", "见连接页"
				case !hs.Enabled && hs.Phase != app.HostActive:
					col, msg, sub = th.TextQuaternary, "未开启被控", ""
				case hs.Phase == app.HostActive && hs.FilesOnly:
					col, msg, sub = th.Error.Base, "正在传输文件", ""
				case hs.Phase == app.HostActive:
					col, msg, sub = th.Error.Base, "正在被控制", ""
				}
				dot(c, col, 6)
				ui.Text(c, msg).Size(12).Secondary().NoWrap()
				if sub != "" {
					ui.Text(c, "· "+sub).Size(12).Tertiary().NoWrap()
				}
			}, layout.Gap(6), layout.Middle())
		}, layout.Gap(10), layout.Padding(insets(12, 16, 12, 16))).Paint(topLine(th.Split))
	}, layout.W(200), layout.Padding(insets(12, 0, 0, 0))).Bg(th.BgContainer).Paint(endLine(th.Split))
}

// connect dials in the background and opens the session window, or only
// the file window in 传文件 mode. A successful connection adds the address
// to 我的设备 (or updates its 最近连接).
func (m *Main) connect(addr, pw, mode string) {
	m.mu.Lock()
	m.connecting, m.connErr, m.connFrom = true, "", m.nav
	m.mu.Unlock()
	viewOnly := mode == modeView || m.o.ViewOnly
	filesOnly := mode == modeTransfer
	st := m.o.Store.Settings()
	v := app.NewViewer(app.ViewerConfig{Addr: addr, Password: pw, ViewOnly: viewOnly, FilesOnly: filesOnly, Log: m.o.ViewerLog,
		Clipboard: m.clipboard(),
		FPS:       st.FPS, BitrateBps: st.BitrateMbps * 1_000_000,
		Remote: rendezvous.Config{URL: st.RemoteURL, Token: st.RemoteToken, PublicKey: st.RemotePublicKey}}, nil)
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
		if !rendezvous.IsCode(addr) {
			if _, err := m.o.Store.Connected(v.Addr(), time.Now()); err != nil {
				log.Printf("kkdesk: address book: %v", err)
			}
			m.syncProbeTargets()
		}
		m.o.App.Post(func() {
			m.addRecent(Recent{Addr: addr, At: time.Now(), ViewOnly: viewOnly && !filesOnly, Files: filesOnly})
			onEnd := func(msg string) {
				if msg != "" {
					m.ShowError(msg)
				}
			}
			open := OpenSession
			if filesOnly {
				open = func(a *tango.App, v *app.Viewer, onEnd func(string)) error { return OpenFiles(a, v, true, onEnd) }
			}
			if err := open(m.o.App, v, onEnd); err != nil {
				log.Printf("kkdesk: session window: %v", err)
				v.Close()
			}
			m.Invalidate()
		})
	}()
}

// clipboard is the clipboard for viewers, or nil (a nil *Clipboard must not
// become a non-nil interface).
func (m *Main) clipboard() clipsync.Clipboard {
	if m.o.Clipboard == nil {
		return nil
	}
	return m.o.Clipboard
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

// Package appui is mmdesk's interface (directory internal/ui): the main
// window (this machine's address and password, the form to control another
// machine, the incoming-connection confirmation and the controlled-side
// panel) and the session window (tab strip with stats and 控制中心, the
// remote screen as a native video view). Layout and wording follow the
// prototype outputs/remote-desktop-ui/index.html.
package appui

import (
	"context"
	"fmt"
	"io"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tangoui/tango"
	"github.com/tangoui/tango/ui"
	"github.com/tangoui/tango/ui/layout"
	"github.com/tangoui/tango/ui/theme"
	"github.com/tangoui/tango/uihost"

	"mmdesk/internal/app"
)

// Form is the persisted content of the 控制其他电脑 form.
type Form struct {
	PeerAddr string
	Password string
}

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
	allow   bool // UI thread

	mu         sync.Mutex
	connecting bool
	connErr    string
}

// OpenMain opens the main window. Call on the main thread.
func OpenMain(o MainOptions) (*Main, error) {
	m := &Main{o: o, allow: o.Host.State().Phase != app.HostOff, connErr: o.Error}
	r, s, err := uihost.Open(o.App, uihost.Options{Surface: tango.SurfaceOptions{
		Title:     "mmdesk",
		Width:     960,
		Height:    640,
		MinWidth:  640,
		MinHeight: 480,
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
	hs := m.o.Host.State()
	ui.Column(c, func() {
		ui.Scroll(c, func() {
			ui.Column(c, func() {
				ui.Title(c, "连接", 3)
				ui.Row(c, func() {
					m.hostCard(c, hs)
					m.connectCard(c)
				}, layout.Gap(16), layout.WrapItems())
			}, layout.Gap(16), layout.Pad(24))
		}, layout.Grow(1))
		if hs.Phase == app.HostActive {
			m.controlledPanel(c, hs)
		}
	}, layout.Grow(1))
	m.confirm(c, hs)
}

func (m *Main) hostCard(c *ui.Context, hs app.HostState) {
	ui.Card(c, "允许控制本机").Layout(layout.Grow(1), layout.Basis(320)).Body(func() {
		ui.Column(c, func() {
			ui.Paragraph(c, "把本机地址和临时密码告诉对方，对方就能连接这台电脑。").Secondary()
			ui.Text(c, "本机地址").Secondary()
			if len(hs.Addrs) == 0 {
				ui.Text(c, "没有可用的网络地址")
			}
			for _, a := range hs.Addrs {
				ui.Text(c, fmt.Sprintf("%s:%d", a, hs.Port)).Code().Copyable()
			}
			ui.Text(c, "临时密码").Secondary()
			ui.Row(c, func() {
				ui.Text(c, hs.Password).Code().Size(32).Strong()
				if ui.Button(c, "刷新").Clicked() {
					go m.o.Host.RefreshPassword()
				}
			}, layout.Gap(12), layout.Middle())
			ui.Row(c, func() {
				if ui.Switch(c, &m.allow).Changed() {
					on := m.allow
					go m.o.Host.SetEnabled(on)
				}
				ui.Text(c, "允许被远程控制")
			}, layout.Gap(8), layout.Middle())
			switch hs.Phase {
			case app.HostListening:
				ui.Text(c, "等待连接…").Secondary()
			case app.HostPending:
				ui.Text(c, hs.Peer+" 请求控制本机").Secondary()
			case app.HostActive:
				ui.Text(c, "正在被 "+hs.Peer+" 控制").Secondary()
			}
			if hs.Err != "" {
				ui.Alert(c, "warning", hs.Err).ShowIcon(true)
			} else if hs.Notice != "" {
				ui.Text(c, hs.Notice).Tertiary()
			}
		}, layout.Gap(10))
	})
}

func (m *Main) connectCard(c *ui.Context) {
	m.mu.Lock()
	connecting, errMsg := m.connecting, m.connErr
	m.mu.Unlock()
	st := m.o.Form
	ui.Card(c, "控制其他电脑").Layout(layout.Grow(1), layout.Basis(320)).Body(func() {
		f := ui.Form(c).Vertical()
		f.Body(func() {
			f.Item("对方地址").Body(func() { ui.Input(c, &st.PeerAddr).Placeholder("192.168.1.20") })
			f.Item("临时密码").Body(func() { ui.Input(c, &st.Password).Placeholder("6 位数字") })
			f.Item("").NoLabel().Body(func() {
				b := ui.Button(c, "连接").Primary().Loading(connecting).Disabled(strings.TrimSpace(st.PeerAddr) == "")
				if b.Clicked() && !connecting {
					m.connect(strings.TrimSpace(st.PeerAddr), strings.TrimSpace(st.Password))
				}
			})
			if errMsg != "" {
				f.Item("").NoLabel().Body(func() { ui.Alert(c, "error", errMsg).ShowIcon(true) })
			}
		})
	})
}

// connect dials in the background and opens the session window.
func (m *Main) connect(addr, pw string) {
	m.mu.Lock()
	m.connecting, m.connErr = true, ""
	m.mu.Unlock()
	v := app.NewViewer(app.ViewerConfig{Addr: addr, Password: pw, ViewOnly: m.o.ViewOnly, Log: m.o.ViewerLog}, nil)
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
			if err := OpenSession(m.o.App, v, func(msg string) {
				if msg != "" {
					m.ShowError(msg)
				}
			}); err != nil {
				log.Printf("mmdesk: session window: %v", err)
				v.Close()
			}
		})
	}()
}

// confirm is the incoming-connection dialog.
func (m *Main) confirm(c *ui.Context, hs app.HostState) {
	open := hs.Phase == app.HostPending
	if !open {
		return
	}
	left := max(0, int(time.Until(hs.Deadline).Round(time.Second)/time.Second))
	c.WakeAt(time.Now().Add(time.Second))
	md := ui.Modal(c, &open, "连接请求").OkText("接受").CancelText("拒绝").Width(min(420, c.Size().W-32)).Body(func() {
		ui.Paragraph(c, fmt.Sprintf("%s 请求控制本机。", hs.Peer))
		ui.Text(c, fmt.Sprintf("%d 秒后自动拒绝", left)).Secondary()
	})
	switch {
	case md.Ok():
		go m.o.Host.Accept()
	case md.Cancelled() || !open:
		go m.o.Host.Reject()
	}
}

// controlledPanel is the bottom-right card shown while being controlled.
func (m *Main) controlledPanel(c *ui.Context, hs app.HostState) {
	el := time.Since(hs.Since).Truncate(time.Second)
	c.WakeAt(time.Now().Add(time.Second))
	ui.Row(c, func() {
		ui.Card(c, "正在被远程控制").Small().Layout(layout.W(300)).Body(func() {
			ui.Column(c, func() {
				ui.Text(c, "对方："+hs.Peer)
				ui.Text(c, "已连接 "+formatElapsed(el)).Secondary()
				if ui.Button(c, "断开").Danger().Clicked() {
					go m.o.Host.Disconnect()
				}
			}, layout.Gap(8))
		})
	}, layout.JustifyContent(layout.JustifyEnd), layout.Pad(16))
}

func formatElapsed(d time.Duration) string {
	s := int(d / time.Second)
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, s/60%60, s%60)
	}
	return fmt.Sprintf("%02d:%02d", s/60, s%60)
}

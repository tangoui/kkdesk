package appui

import (
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tangoui/tango"
	"github.com/tangoui/tango/sys/menu"
	"github.com/tangoui/tango/ui"
	"github.com/tangoui/tango/ui/geom"
	"github.com/tangoui/tango/ui/icon"
	"github.com/tangoui/tango/ui/icon/antd"
	tin "github.com/tangoui/tango/ui/input"
	"github.com/tangoui/tango/ui/layout"
	"github.com/tangoui/tango/ui/theme"
	"github.com/tangoui/tango/uihost"

	"github.com/tangoui/kkdesk/internal/app"
	"github.com/tangoui/kkdesk/internal/display"
	"github.com/tangoui/kkdesk/internal/input"
)

// closeDelay keeps the session window up briefly after the session ends so
// that "连接已断开" can be read.
const closeDelay = 1500 * time.Millisecond

// Quality levels of the 控制中心 menu (bps, 0 = auto) and frame rates.
var (
	bitrates = []struct {
		label string
		bps   int
	}{{"自动", 0}, {"30 Mbps", 30_000_000}, {"14 Mbps", 14_000_000}, {"8 Mbps", 8_000_000}, {"3 Mbps", 3_000_000}}
	frameRates = []int{30, 60, 90, 144}
)

// sessionWin is one session window: a tab strip and the remote screen.
type sessionWin struct {
	app     *tango.App
	v       *app.Viewer
	runner  atomic.Pointer[ui.Runner]
	surface atomic.Pointer[tango.Surface]

	// UI thread
	focused  bool
	pressed  [8]bool // buttons pressed inside the video, released remotely
	menuOpen atomic.Bool
}

// OpenSession opens the session window for a connected viewer. onEnd gets
// the end reason ("" for 退出远控 or closing the window) after the window
// closes. Call on the main thread.
func OpenSession(a *tango.App, v *app.Viewer, onEnd func(msg string)) error {
	w := &sessionWin{app: a, v: v}
	r, s, err := uihost.Open(a, uihost.Options{Surface: tango.SurfaceOptions{
		Title:     "kkdesk - " + v.Addr(),
		Width:     1280,
		Height:    800,
		MinWidth:  480,
		MinHeight: 320,
		Resizable: true,
		Centered:  true,
	}}, w.page, ui.RunnerOptions{Theme: theme.AntDesign(theme.Dark())})
	if err != nil {
		return err
	}
	w.runner.Store(r)
	w.surface.Store(s)
	v.AddNotify(r.Invalidate)
	var once sync.Once
	finish := func() {
		once.Do(func() {
			if onEnd != nil {
				onEnd(v.State().Err)
			}
		})
	}
	s.OnClosed(func() {
		v.Close()
		v.OnEnd(func() { a.Post(finish) })
	})
	v.OnEnd(func() {
		r.Invalidate()
		time.AfterFunc(closeDelay, func() { a.Post(s.Close) })
	})
	return nil
}

func (w *sessionWin) invalidate() {
	if r := w.runner.Load(); r != nil {
		r.Invalidate()
	}
}

func (w *sessionWin) page(c *ui.Context) {
	st := w.v.State()
	ui.Column(c, func() {
		w.strip(c, st)
		w.screen(c, st)
	}, layout.Grow(1)).Bg(theme.Hex("#000000"))
}

// strip is the tab bar (always dark, the window uses the dark theme): the
// peer's tab on the left, stats and 控制中心 on the right.
func (w *sessionWin) strip(c *ui.Context, st app.ViewerState) {
	th := c.Theme
	ui.Row(c, func() {
		w.tab(c, st)
		ui.Row(c, func() { w.phaseTags(c, st) }, layout.Gap(6), layout.Middle(), layout.Self(layout.AlignCenter), layout.Padding(insets(0, 0, 4, 0)))
		ui.Spacer(c)
		ui.Row(c, func() {
			if st.Phase == app.ViewerStreaming {
				s := st.Stats
				ms := int(s.LatencyP50.Milliseconds())
				dot(c, th.Success.Base, 6)
				mono(c, fmt.Sprintf("%d ms", ms), 12, 500, latencyColor(th, ms))
				mono(c, fmt.Sprintf("%.0f fps · %.1f Mbps · 直连", s.FPS, s.BitrateBps/1e6), 12, 400, th.TextTertiary)
				c.WakeAt(time.Now().Add(500 * time.Millisecond))
			}
			b := ui.Button(c, "控制中心").Icon(icon.Sliders).Small().Disabled(st.Phase == app.ViewerClosed)
			if b.Clicked() {
				if r, ok := c.RectOf(b.ID()); ok {
					w.popupMenu(int(r.X), int(r.Y+r.H+4), st)
				}
			}
		}, layout.Gap(8), layout.Middle(), layout.Self(layout.AlignCenter), layout.Padding(insets(0, 0, 4, 0)))
	}, layout.Gap(8), layout.Padding(insets(6, 8, 0, 8)), layout.H(38), layout.AlignItems(layout.AlignEnd)).Bg(th.BgElevated)
}

// phaseTags shows the session state next to the tab.
func (w *sessionWin) phaseTags(c *ui.Context, st app.ViewerState) {
	switch st.Phase {
	case app.ViewerConnecting, app.ViewerWaiting:
		ui.Tag(c, "等待对方确认…").Color("processing")
	case app.ViewerClosed:
		msg := st.Err
		if msg == "" {
			msg = "连接已断开"
		}
		ui.Tag(c, msg).Color("error")
	}
	if st.ViewOnly {
		ui.Tag(c, "仅查看").Color("blue")
	}
}

// tab is the active session tab: device icon, address, latency and ×.
func (w *sessionWin) tab(c *ui.Context, st app.ViewerState) {
	th := c.Theme
	ui.Row(c, func() {
		ui.Icon(c, antd.DesktopOutlined).Size(14).Color(th.TextSecondary)
		ui.Text(c, st.Addr).Size(13).Color(th.Text).NoWrap()
		if st.Phase == app.ViewerStreaming {
			ms := int(st.Stats.LatencyP50.Milliseconds())
			mono(c, fmt.Sprintf("%d ms", ms), 11, 400, latencyColor(th, ms))
		}
		if ui.IconButton(c, icon.Close, "关闭会话").Size(18, 12).Clicked() {
			w.v.Close()
		}
	}, layout.Gap(8), layout.Middle(), layout.H(32), layout.PadSE(0, 12, 8)).
		Bg(theme.Hex("#000000")).Radii(geom.Radii{TL: 6, TR: 6})
}

// latencyColor colors a latency: green below 30 ms, gold below 80, red.
func latencyColor(th *theme.Theme, ms int) theme.Color {
	switch {
	case ms < 30:
		return th.Success.Base
	case ms < 80:
		return th.Warning.Base
	}
	return th.Error.Base
}

// popupMenu shows the 控制中心 native menu below the button.
func (w *sessionWin) popupMenu(x, y int, st app.ViewerState) {
	s := w.surface.Load()
	if s == nil || w.menuOpen.Swap(true) {
		return
	}
	v := w.v
	m := menu.New()
	q := m.Submenu("画质")
	for _, b := range bitrates {
		q.Radio(b.label, "bitrate", st.BitrateBps == b.bps, func() { v.SetQuality(b.bps, st.FPS) })
	}
	f := m.Submenu("帧率")
	for _, fps := range frameRates {
		f.Radio(fmt.Sprintf("%d fps", fps), "fps", st.FPS == fps, func() { v.SetQuality(st.BitrateBps, fps) })
	}
	m.Separator()
	m.Check("真彩", false, nil).SetDisabled(true)
	m.Check("HDR", false, nil).SetDisabled(true)
	m.Check("中继连接", false, nil).SetDisabled(true)
	m.Separator()
	m.Check("仅查看", st.ViewOnly, func() { v.SetViewOnly(!st.ViewOnly) })
	m.Separator()
	m.Add("退出远控", v.Close)
	// Popup blocks until the menu closes; off the UI thread so the window
	// keeps drawing (the video is a native view and keeps playing anyway).
	go func() {
		defer w.menuOpen.Store(false)
		if err := menu.Popup(s, x, y, m); err != nil {
			log.Printf("kkdesk viewer: menu: %v", err)
		}
		w.invalidate()
	}()
}

// screen is the remote screen: a native video view over a box that reads
// the pointer and keyboard and forwards them.
func (w *sessionWin) screen(c *ui.Context, st app.ViewerState) {
	nh := uihost.NativeHost(c, "remote", uihost.NativeHostOptions{
		Create: func() (uintptr, error) {
			view, err := display.New()
			if err != nil {
				return 0, err
			}
			w.v.SetView(view)
			return view.Handle(), nil
		},
		Release: func(uintptr) {
			view := w.v.View()
			w.v.SetView(nil)
			if view != nil {
				view.Release()
			}
		},
		PassInput: true,
		Covered:   uihost.CoverIgnore,
	}, layout.Grow(1))
	nh.Bg(theme.Hex("#000000"))
	// PassInput takes the box out of the Tab order; the remote screen still
	// needs keyboard focus to forward keys, so make it focusable again and
	// focus it (on open and on every click).
	nh.Focusable(true).A11yLabel("远程屏幕")
	id := nh.ID()
	if !w.focused || nh.Clicked() {
		w.focused = true
		c.SetFocus(id)
	}
	if err := nh.Err(); err != nil {
		ui.Text(c, "无法显示远程画面："+err.Error())
	}
	if st.Phase == app.ViewerClosed {
		return
	}
	c.CaptureTab(id)
	w.forwardInput(c, id)
}

// forwardInput maps this frame's pointer, wheel and key events on the box
// to remote input events.
func (w *sessionWin) forwardInput(c *ui.Context, id ui.ID) {
	box, ok := c.RectOf(id)
	keys := c.EventsFor(id)
	wdx, wdy, wheel := c.WheelFor(id)
	if !ok || w.v.ViewOnly() {
		w.pressed = [8]bool{}
		return
	}
	fw, fh := w.v.FrameSize()
	norm := func(p tin.Pointer) (float32, float32, bool) {
		return display.Normalize(p.Pos.X-box.X, p.Pos.Y-box.Y, box.W, box.H, fw, fh)
	}
	for _, p := range c.PointerEvents() {
		nx, ny, in := norm(p)
		btn := uint8(p.Button)
		switch p.Kind {
		case tin.Move:
			if in || w.anyPressed() {
				w.v.Send(input.Event{Kind: input.Move, X: nx, Y: ny})
			}
		case tin.Press:
			if in && int(btn) < len(w.pressed) {
				w.pressed[btn] = true
				w.v.Send(input.Event{Kind: input.Button, X: nx, Y: ny, Button: btn, Down: true, Mods: uint8(p.Mods)})
			}
		case tin.Release, tin.Cancel:
			if int(btn) < len(w.pressed) && w.pressed[btn] {
				w.pressed[btn] = false
				w.v.Send(input.Event{Kind: input.Button, X: nx, Y: ny, Button: btn, Mods: uint8(p.Mods)})
			}
		}
	}
	if wheel {
		if pt, inWin := c.Pointer(); inWin {
			if _, _, in := display.Normalize(pt.X-box.X, pt.Y-box.Y, box.W, box.H, fw, fh); in {
				// Tango: positive DY reveals content below; the wire uses the
				// CGScrollWheel convention (positive scrolls up), so negate.
				w.v.Send(input.Event{Kind: input.Wheel, DX: -wdx, DY: -wdy, Mods: uint8(c.PointerMods())})
			}
		}
	}
	for _, e := range keys {
		k, ok := e.(tin.KeyEvent)
		if !ok || k.HID == 0 {
			continue
		}
		w.v.Send(input.Event{Kind: input.Key, HID: k.HID, Down: k.Down, Mods: uint8(k.Mods)})
	}
}

func (w *sessionWin) anyPressed() bool {
	for _, p := range w.pressed {
		if p {
			return true
		}
	}
	return false
}

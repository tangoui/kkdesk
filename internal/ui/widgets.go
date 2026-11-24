package appui

import (
	"math"
	"strconv"
	"time"

	"github.com/tangoui/tango/ui"
	"github.com/tangoui/tango/ui/geom"
	"github.com/tangoui/tango/ui/layout"
	"github.com/tangoui/tango/ui/paint"
	"github.com/tangoui/tango/ui/text"
	"github.com/tangoui/tango/ui/theme"
)

// mono draws one line of monospace text without the code box that
// ui.Text(...).Code() paints (addresses, passwords, stats).
func mono(c *ui.Context, s string, size float32, weight text.Weight, col theme.Color, opts ...layout.Option) *ui.Box {
	st := text.Style{Size: size, Weight: weight, Mono: true, LineHeight: c.Theme.LineHeightFor(size)}
	lay := text.Shape(s, st, text.Options{NoWrap: true})
	sz := geom.Size{W: float32(math.Ceil(float64(lay.Width))), H: lay.Height}
	b := ui.Custom(c, func(_, _ float32) geom.Size { return sz }, func(l *paint.List, r geom.Rect) {
		l.Glyphs(geom.Pt(r.X, r.Y+(r.H-lay.Height)/2), lay.Glyphs, col)
	}, opts...)
	b.Label(s)
	return b
}

// dot is a small status dot.
func dot(c *ui.Context, col theme.Color, size float32) {
	ui.Custom(c, func(_, _ float32) geom.Size { return geom.Size{W: size, H: size} }, func(l *paint.List, r geom.Rect) {
		l.Fill(r, geom.Rad(size/2), col)
	}, layout.NoStretch())
}

// pulseDot is a dot that fades in and out (being controlled).
func pulseDot(c *ui.Context, col theme.Color, size float32) {
	const period = 1600 * time.Millisecond
	ph := float64(c.Now().UnixMilli()%period.Milliseconds()) / float64(period.Milliseconds())
	a := 0.675 + 0.325*math.Cos(2*math.Pi*ph) // 1 → .35 → 1
	c.WakeAt(c.Now().Add(50 * time.Millisecond))
	dot(c, theme.Fade(col, a), size)
}

// panel is a bordered surface (the prototype's .card) without a header.
func panel(c *ui.Context, fn func(), opts ...layout.Option) *ui.Box {
	th := c.Theme
	return ui.Column(c, fn, opts...).Bg(th.BgContainer).Border(1, th.BorderSecondary).Radius(th.RadiusLG)
}

// hline is a 1px horizontal divider in the split color.
func hline(c *ui.Context) {
	col := c.Theme.Split
	ui.Custom(c, func(maxW, _ float32) geom.Size { return geom.Size{W: 0, H: 1} }, func(l *paint.List, r geom.Rect) {
		l.Fill(r, geom.Radii{}, col)
	}, layout.H(1), layout.Self(layout.AlignStretch))
}

// endLine paints a 1px line along the right edge of a box (sider border).
func endLine(col theme.Color) ui.PaintFunc {
	return func(l *paint.List, r geom.Rect) {
		l.Fill(geom.Rect{X: r.X + r.W - 1, Y: r.Y, W: 1, H: r.H}, geom.Radii{}, col)
	}
}

// topLine paints a 1px line along the top edge of a box.
func topLine(col theme.Color) ui.PaintFunc {
	return func(l *paint.List, r geom.Rect) {
		l.Fill(geom.Rect{X: r.X, Y: r.Y, W: r.W, H: 1}, geom.Radii{}, col)
	}
}

func insets(top, right, bottom, left float32) geom.Insets {
	return geom.Insets{Top: top, Right: right, Bottom: bottom, Left: left}
}

func itoa(n int) string { return strconv.Itoa(n) }

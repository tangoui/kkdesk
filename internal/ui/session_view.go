package appui

import (
	"math"

	"github.com/tangoui/tango/sys/menu"
	"github.com/tangoui/tango/ui"
	"github.com/tangoui/tango/ui/geom"
	"github.com/tangoui/tango/ui/layout"

	"github.com/tangoui/kkdesk/internal/display"
)

type sessionViewport struct {
	actual     bool
	panX, panY float64 // percent of the overflow on each axis
}

func (v *sessionViewport) render(c *ui.Context, space geom.Size, frameW, frameH int, video func(ui.ID, ...layout.Option)) {
	actual := v.actual && frameW > 0 && frameH > 0
	scale := c.PixelScale()
	width, height := float32(frameW)/scale, float32(frameH)/scale
	const panBarHeight = 32
	showPan := actual && (width > space.W || height > space.H)
	if showPan {
		space.H -= panBarHeight
	}
	body := ui.Div(c, layout.Grow(1), layout.MinH(0), layout.MinW(0)).Clip()
	body.Body(func() {
		if !actual {
			video(body.ID(), layout.Fill())
			return
		}
		x := videoOffset(space.W, width, scale, v.panX)
		y := videoOffset(space.H, height, scale, v.panY)
		video(body.ID(), layout.Abs(x, y, layout.Unset, layout.Unset), layout.Size(width, height), layout.Physical())
	})
	if showPan {
		ui.Row(c, func() {
			if width > space.W {
				ui.Text(c, "水平").Size(12)
				ui.Slider(c, &v.panX, 0, 100).A11yLabel("水平平移").TooltipOpen(false).Layout(layout.Grow(1), layout.MinW(0)).Changed()
			}
			if height > space.H {
				ui.Text(c, "垂直").Size(12)
				ui.Slider(c, &v.panY, 0, 100).A11yLabel("垂直平移").TooltipOpen(false).Layout(layout.Grow(1), layout.MinW(0)).Changed()
			}
		}, layout.H(panBarHeight), layout.Shrink(0), layout.PadVH(0, 12), layout.Gap(12), layout.Middle()).Bg(c.Theme.BgElevated)
	}
}

func videoOffset(viewport, video, scale float32, pan float64) float32 {
	offset := (viewport - video) / 2
	if video > viewport {
		offset = (viewport - video) * float32(pan/100)
	}
	return float32(math.Round(float64(offset*scale))) / scale
}

func videoPointer(point geom.Point, box, viewport geom.Rect, frameW, frameH int) (x, y float32, inside bool) {
	x, y, inside = display.Normalize(point.X-box.X, point.Y-box.Y, box.W, box.H, frameW, frameH)
	return x, y, inside && viewport.Contains(point)
}

func addWindowMenu(m *menu.Menu, actual, fullscreen bool, setActual func(bool), toggleFullscreen func()) {
	window := m.Submenu("窗口")
	window.Radio("scale", "自适应", !actual, func() { setActual(false) })
	window.Radio("scale", "实际大小 1:1", actual, func() { setActual(true) })
	window.Separator()
	label := "进入全屏"
	if fullscreen {
		label = "退出全屏"
	}
	window.Add(label, toggleFullscreen)
}

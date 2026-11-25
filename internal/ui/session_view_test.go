package appui

import (
	"math"
	"testing"

	"github.com/tangoui/tango/sys/menu"
	"github.com/tangoui/tango/ui"
	"github.com/tangoui/tango/ui/geom"
	"github.com/tangoui/tango/ui/layout"
)

func TestActualSizeLayoutAndPointer(t *testing.T) {
	view := sessionViewport{actual: true, panX: 100, panY: 100}
	driver := ui.NewDriver(func(c *ui.Context) {
		ui.Column(c, func() {
			view.render(c, c.Size(), 1920, 1080, func(_ ui.ID, opts ...layout.Option) {
				ui.NativeView(c, "remote", opts...)
			})
		}, layout.Grow(1))
	}, ui.DriverOptions{Width: 700, Height: 500, Scale: 2})
	driver.Settle()
	regions := driver.Runner().NativeRegions()
	if len(regions) != 1 {
		t.Fatalf("native regions = %+v", regions)
	}
	region := regions[0]
	if region.Rect.W != 960 || region.Rect.H != 540 {
		t.Fatalf("1920×1080 at 2 device pixels per point: %+v", region)
	}
	if region.Rect.Right() != region.Visible.Right() || region.Rect.Bottom() != region.Visible.Bottom() ||
		region.Visible.W >= region.Rect.W || region.Visible.H >= region.Rect.H {
		t.Fatalf("bottom-right of image should be reachable at maximum pan: %+v", region)
	}
	p := geom.Pt(region.Visible.X, region.Visible.Y)
	x, y, inside := videoPointer(p, region.Rect, region.Visible, 1920, 1080)
	wantX := (p.X - region.Rect.X) / region.Rect.W
	wantY := (p.Y - region.Rect.Y) / region.Rect.H
	if !inside || math.Abs(float64(x-wantX)) > 1e-5 || math.Abs(float64(y-wantY)) > 1e-5 {
		t.Fatalf("video point = (%v, %v, %v), want (%v, %v, true)", x, y, inside, wantX, wantY)
	}
	if _, _, inside := videoPointer(geom.Pt(p.X+2, region.Visible.Bottom()+1), region.Rect, region.Visible, 1920, 1080); inside {
		t.Fatal("a pointer outside the visible video was treated as remote input")
	}
	view.actual = false
	driver.Settle()
	region = driver.Runner().NativeRegions()[0]
	if region.Rect != (geom.Rect{W: 700, H: 500}) || region.Visible != region.Rect {
		t.Fatalf("fit mode should occupy the viewport: %+v", region)
	}
}

func TestWindowMenuShowsCurrentScaleAndFullscreenAction(t *testing.T) {
	for _, fullscreen := range []bool{false, true} {
		m := menu.New()
		selected, toggled := false, false
		addWindowMenu(m, true, fullscreen, func(actual bool) { selected = !actual }, func() { toggled = true })
		items := m.Items[0].Submenu.Items
		if items[0].Label != "自适应" || items[0].Checked || items[1].Label != "实际大小 1:1" || !items[1].Checked {
			t.Fatalf("unexpected scale choices: %+v", items)
		}
		want := "进入全屏"
		if fullscreen {
			want = "退出全屏"
		}
		if items[3].Label != want {
			t.Fatalf("fullscreen action = %q, want %q", items[3].Label, want)
		}
		items[0].OnClick()
		items[3].OnClick()
		if !selected || !toggled {
			t.Fatal("window menu actions were not wired")
		}
	}
}

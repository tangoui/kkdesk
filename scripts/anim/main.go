// Command anim keeps a large part of the screen changing every frame, the
// content the loopback measurement captures (scripts/loopback.sh): moving
// coloured bars and a millisecond clock (for photo-based glass-to-glass
// checks).
package main

import (
	"flag"
	"fmt"
	"image/color"
	"log"
	"math"
	"time"

	"github.com/tangoui/tango"
	"github.com/tangoui/tango/ui"
	"github.com/tangoui/tango/ui/layout"
	"github.com/tangoui/tango/uihost"
)

func main() {
	ww := flag.Int("w", 1600, "window width")
	wh := flag.Int("h", 900, "window height")
	flag.Parse()
	quit := true
	app := tango.New(tango.Options{Name: "anim", ID: "com.mmdesk.anim", QuitOnLastClose: &quit})
	start := time.Now()
	// Report the real render rate once per second on stdout, so the
	// loopback summary can compare it with what capture delivers.
	frames, sec := 0, time.Now()
	app.OnReady(func() {
		_, _, err := uihost.Open(app, uihost.Options{Surface: tango.SurfaceOptions{Title: "mmdesk anim", Width: *ww, Height: *wh, Centered: true,
			// Floating level without activating the app (no focus steal):
			// a window opened behind others may be occluded and throttled.
			Appearance: tango.Appearance{AlwaysOnTop: true}}}, func(c *ui.Context) {
			t := c.Now().Sub(start).Seconds()
			w, h := float32(*ww), float32(*wh)
			for i := 0; i < 12; i++ {
				x := float32(math.Mod(t*300+float64(i)*140, float64(w)+200)) - 200
				hue := math.Mod(t*60+float64(i)*30, 360)
				ui.Div(c, layout.Abs(x, float32(i)*h/12, layout.Unset, layout.Unset), layout.Size(200, h/12)).Bg(hsv(hue))
			}
			ui.Column(c, func() {
				ui.Title(c, fmt.Sprintf("%.3f s", time.Since(start).Seconds()), 1)
			}, layout.Abs(40, 40, layout.Unset, layout.Unset))
			frames++
			if now := time.Now(); now.Sub(sec) >= time.Second {
				fmt.Printf("anim fps %.1f\n", float64(frames)/now.Sub(sec).Seconds())
				frames, sec = 0, now
			}
			// c.Invalidate only re-runs the build pass of the current frame;
			// the Invalidator asks the window for the next (vsync) frame.
			// The old c.Invalidate() call left anim at ~0.4 fps.
			c.Invalidator()()
		}, ui.RunnerOptions{})
		if err != nil {
			log.Fatal(err)
		}
	})
	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}

func hsv(h float64) color.NRGBA {
	c := 1.0
	x := c * (1 - math.Abs(math.Mod(h/60, 2)-1))
	var r, g, b float64
	switch {
	case h < 60:
		r, g = c, x
	case h < 120:
		r, g = x, c
	case h < 180:
		g, b = c, x
	case h < 240:
		g, b = x, c
	case h < 300:
		r, b = x, c
	default:
		r, b = c, x
	}
	return color.NRGBA{uint8(r * 255), uint8(g * 255), uint8(b * 255), 255}
}

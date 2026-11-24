package display

import (
	"math"
	"testing"
	"time"
)

func near(a, b float32) bool { return math.Abs(float64(a-b)) < 1e-4 }

func TestVideoRect(t *testing.T) {
	cases := []struct {
		name       string
		bw, bh     float32
		fw, fh     int
		x, y, w, h float32
	}{
		{"16:9 in 4:3 letterbox", 800, 600, 1920, 1080, 0, 75, 800, 450},
		{"4:3 in 16:9 pillarbox", 1600, 900, 1024, 768, 200, 0, 1200, 900},
		{"exact fit", 1920, 1080, 1920, 1080, 0, 0, 1920, 1080},
		{"exact aspect scaled", 960, 540, 1920, 1080, 0, 0, 960, 540},
		{"zero frame", 800, 600, 0, 0, 0, 0, 0, 0},
		{"zero frame height", 800, 600, 1920, 0, 0, 0, 0, 0},
		{"zero box", 0, 0, 1920, 1080, 0, 0, 0, 0},
		{"negative box", -10, 600, 1920, 1080, 0, 0, 0, 0},
	}
	for _, c := range cases {
		x, y, w, h := VideoRect(c.bw, c.bh, c.fw, c.fh)
		if !near(x, c.x) || !near(y, c.y) || !near(w, c.w) || !near(h, c.h) {
			t.Errorf("%s: got (%v,%v,%v,%v) want (%v,%v,%v,%v)", c.name, x, y, w, h, c.x, c.y, c.w, c.h)
		}
	}
}

func TestNormalize(t *testing.T) {
	cases := []struct {
		name   string
		px, py float32
		bw, bh float32
		fw, fh int
		nx, ny float32
		inside bool
	}{
		// 16:9 in 800x600: video at y 75..525.
		{"letterbox center", 400, 300, 800, 600, 1920, 1080, 0.5, 0.5, true},
		{"letterbox top-left of video", 0, 75, 800, 600, 1920, 1080, 0, 0, true},
		{"letterbox bottom-right of video", 800, 525, 800, 600, 1920, 1080, 1, 1, true},
		{"letterbox top bar", 400, 10, 800, 600, 1920, 1080, 0.5, 0, false},
		{"letterbox bottom bar", 200, 590, 800, 600, 1920, 1080, 0.25, 1, false},
		// 4:3 in 1600x900: video at x 200..1400.
		{"pillarbox quarter", 500, 225, 1600, 900, 1024, 768, 0.25, 0.25, true},
		{"pillarbox left bar", 100, 450, 1600, 900, 1024, 768, 0, 0.5, false},
		{"pillarbox right bar", 1500, 450, 1600, 900, 1024, 768, 1, 0.5, false},
		{"exact fit", 480, 810, 1920, 1080, 1920, 1080, 0.25, 0.75, true},
		{"outside box negative", -5, -5, 1920, 1080, 1920, 1080, 0, 0, false},
		{"zero frame", 10, 10, 800, 600, 0, 0, 0, 0, false},
		{"zero box", 10, 10, 0, 0, 1920, 1080, 0, 0, false},
	}
	for _, c := range cases {
		nx, ny, in := Normalize(c.px, c.py, c.bw, c.bh, c.fw, c.fh)
		if !near(nx, c.nx) || !near(ny, c.ny) || in != c.inside {
			t.Errorf("%s: got (%v,%v,%v) want (%v,%v,%v)", c.name, nx, ny, in, c.nx, c.ny, c.inside)
		}
	}
}

func TestNextTick(t *testing.T) {
	ref := time.Unix(1000, 0)
	p := 10 * time.Millisecond
	cases := []struct {
		after time.Duration // relative to ref
		want  time.Duration
	}{
		{0, 0},
		{1, p},
		{p, p},
		{p + 1, 2 * p},
		{35 * time.Millisecond, 40 * time.Millisecond},
		{-1, 0},
		{-p, -p},
		{-p - 1, -p},
		{-15 * time.Millisecond, -10 * time.Millisecond},
	}
	for _, c := range cases {
		if got := nextTick(ref, p, ref.Add(c.after)); !got.Equal(ref.Add(c.want)) {
			t.Errorf("nextTick(after=%v) = %v, want %v", c.after, got.Sub(ref), c.want)
		}
	}
}

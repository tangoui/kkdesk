package capture

import (
	"testing"
	"time"
)

func TestScaledSize(t *testing.T) {
	cases := []struct {
		w, h, max    int
		wantW, wantH int
	}{
		{3840, 2160, 1920, 1920, 1080},
		{2560, 1600, 1920, 1920, 1200},
		{1366, 768, 1920, 1366, 768},
		{3024, 1964, 1920, 1920, 1246},
		{1365, 767, 1920, 1364, 766},
		{3840, 2160, 0, 3840, 2160},
		{1920, 1080, 1920, 1920, 1080},
	}
	for _, c := range cases {
		w, h := scaledSize(c.w, c.h, c.max)
		if w != c.wantW || h != c.wantH {
			t.Errorf("scaledSize(%d,%d,%d) = %dx%d, want %dx%d", c.w, c.h, c.max, w, h, c.wantW, c.wantH)
		}
		if w%2 != 0 || h%2 != 0 {
			t.Errorf("scaledSize(%d,%d,%d) = %dx%d not even", c.w, c.h, c.max, w, h)
		}
	}
}

func TestContentTime(t *testing.T) {
	cases := []struct {
		name             string
		display, arrival uint64
		want             uint64
		late             bool
	}{
		{"display before arrival", 1_000, 4_000, 1_000, false},
		{"display equals arrival", 4_000, 4_000, 4_000, false},
		// SCK stamps a frame with the vsync it will be shown at, which
		// is typically 3–10 ms after the callback: never report a capture
		// time in the future of the moment we hold the frame.
		{"display after arrival", 9_000, 4_000, 4_000, true},
		{"no display time", 0, 4_000, 4_000, false},
	}
	for _, c := range cases {
		got, late := contentTime(c.display, c.arrival)
		if got != c.want || late != c.late {
			t.Errorf("%s: contentTime(%d,%d) = %d,%v want %d,%v", c.name, c.display, c.arrival, got, late, c.want, c.late)
		}
	}
}

func TestMinFrameInterval(t *testing.T) {
	cases := []struct {
		fps  int
		want time.Duration
	}{
		// 10% below 1/fps: SCK compares against the vsync-to-vsync time,
		// which jitters around 16.67 ms on a 60 Hz panel; exactly 1/60
		// made it skip vsyncs (measured 57 fps, 17.3 ms median interval).
		{60, 15 * time.Millisecond},
		{30, 30 * time.Millisecond},
		{0, 30 * time.Millisecond}, // <= 0 means 30
	}
	for _, c := range cases {
		if got := minFrameInterval(c.fps); got != c.want {
			t.Errorf("minFrameInterval(%d) = %v, want %v", c.fps, got, c.want)
		}
	}
}

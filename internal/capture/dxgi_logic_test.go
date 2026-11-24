package capture

import (
	"testing"
	"time"
)

func TestReopenBackoff(t *testing.T) {
	want := []time.Duration{100 * time.Millisecond, 200 * time.Millisecond, 400 * time.Millisecond,
		800 * time.Millisecond, 1600 * time.Millisecond, 2 * time.Second, 2 * time.Second}
	for i, w := range want {
		if got := reopenBackoff(i); got != w {
			t.Errorf("reopenBackoff(%d) = %v, want %v", i, got, w)
		}
	}
	if got := reopenBackoff(1000); got != 2*time.Second {
		t.Errorf("reopenBackoff(1000) = %v", got)
	}
}

func TestRotatedSize(t *testing.T) {
	cases := []struct{ w, h, rot, ww, wh int }{
		{1920, 1080, dxgiRotationIdentity, 1920, 1080},
		{1920, 1080, dxgiRotation90, 1080, 1920},
		{1920, 1080, dxgiRotation180, 1920, 1080},
		{1920, 1080, dxgiRotation270, 1080, 1920},
		{1920, 1080, 0, 1920, 1080}, // unspecified
	}
	for _, c := range cases {
		if w, h := rotatedSize(c.w, c.h, c.rot); w != c.ww || h != c.wh {
			t.Errorf("rotatedSize(%d,%d,%d) = %dx%d want %dx%d", c.w, c.h, c.rot, w, h, c.ww, c.wh)
		}
	}
}

func TestAcquireTimeoutMs(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want uint32
	}{
		{15 * time.Millisecond, 15},
		{16_700 * time.Microsecond, 17},
		{0, 1},
		{-time.Second, 1},
		{5 * time.Second, 1000}, // capped: Close must not wait long
	}
	for _, c := range cases {
		if got := acquireTimeoutMs(c.d); got != c.want {
			t.Errorf("acquireTimeoutMs(%v) = %d, want %d", c.d, got, c.want)
		}
	}
}

func TestQPCToNs(t *testing.T) {
	cases := []struct {
		qpc, freq int64
		want      uint64
	}{
		{10_000_000, 10_000_000, 1_000_000_000},
		{3, 10_000_000, 300},
		// Large counter values must not overflow (days of uptime at 10 MHz
		// times 1e9 overflows int64 if multiplied first).
		{10_000_000 * 86400 * 30, 10_000_000, 86400 * 30 * 1_000_000_000},
		{0, 0, 0},
	}
	for _, c := range cases {
		if got := qpcToNs(c.qpc, c.freq); got != c.want {
			t.Errorf("qpcToNs(%d,%d) = %d, want %d", c.qpc, c.freq, got, c.want)
		}
	}
}

func TestPresentCaptureNs(t *testing.T) {
	ms := uint64(time.Millisecond)
	cases := []struct {
		name             string
		present, arrival uint64
		want             uint64
		late             bool
	}{
		{"recent present", 1000 * ms, 1010 * ms, 1000 * ms, false},
		{"stale present (first frame)", 1000 * ms, 61000 * ms, 61000 * ms, false},
		{"no present", 0, 1010 * ms, 1010 * ms, false},
		{"present after arrival", 1020 * ms, 1010 * ms, 1010 * ms, true},
	}
	for _, c := range cases {
		got, late := presentCaptureNs(c.present, c.arrival)
		if got != c.want || late != c.late {
			t.Errorf("%s: got %d,%v want %d,%v", c.name, got, late, c.want, c.late)
		}
	}
}

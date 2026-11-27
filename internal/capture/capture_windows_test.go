//go:build windows && cgo

package capture

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/tangoui/kkdesk/internal/winmedia"
)

// After the shared device is replaced (what a display change now does), a
// running capture rebuilds its duplication on the new device and keeps
// delivering frames on it.
func TestCaptureSurvivesDeviceReset(t *testing.T) {
	var frames atomic.Int64
	var lastDevice atomic.Uintptr
	s, err := Start(Options{FPS: 30, MaxWidth: 640}, func(f Frame) {
		frames.Add(1)
		if dev := textureDevice(f.Pixels); dev != 0 {
			lastDevice.Store(dev)
		}
	})
	if err != nil {
		t.Skipf("no desktop to capture here: %v", err)
	}
	defer s.Close()
	waitFrames := func(what string, n int64) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for frames.Load() < n {
			if time.Now().After(deadline) {
				t.Fatalf("%s: %d frames, want %d", what, frames.Load(), n)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	waitFrames("before the reset", 1)
	before, _, _, _ := winmedia.Device()
	winmedia.ResetDevice()
	after, _, _, err := winmedia.Device()
	if err != nil || after == before {
		t.Fatalf("ResetDevice did not replace the device: %#x -> %#x, %v", before, after, err)
	}
	// Frames keep coming (at least one resend a second on a static screen),
	// and they end up on the new device.
	deadline := time.Now().Add(10 * time.Second)
	for lastDevice.Load() != after {
		if time.Now().After(deadline) {
			t.Fatalf("frames still on device %#x, want the new device %#x", lastDevice.Load(), after)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

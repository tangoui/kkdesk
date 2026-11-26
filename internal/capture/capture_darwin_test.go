//go:build darwin && cgo

package capture

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func TestCaptureMainDisplay(t *testing.T) {
	if !HasPermission() {
		t.Skip("Screen Recording permission missing: enable it for the terminal running `go test` " +
			"(System Settings > Privacy & Security > Screen & System Audio Recording)")
	}

	type rec struct {
		w, h   int
		format uint32
		at     time.Time
	}
	var (
		mu     sync.Mutex
		frames []rec
	)
	s, err := Start(Options{FPS: 30, MaxWidth: 1920, ExcludeApp: "kkdesk"}, func(f Frame) {
		r := rec{w: f.Width, h: f.Height, format: pixelFormat(f.Pixels), at: f.CaptureAt}
		mu.Lock()
		frames = append(frames, r)
		mu.Unlock()
	})
	if errors.Is(err, errNoDisplay) {
		t.Skip("ScreenCaptureKit has no display in this login session")
	}
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	w, h := s.Size()
	time.Sleep(time.Second)
	s.SetFPS(60)
	time.Sleep(time.Second)
	s.Close()
	s.Close() // idempotent

	mu.Lock()
	got := append([]rec(nil), frames...)
	mu.Unlock()
	time.Sleep(200 * time.Millisecond)
	mu.Lock()
	after := len(frames)
	mu.Unlock()
	if after != len(got) {
		t.Errorf("received %d frames after Close", after-len(got))
	}

	t.Logf("session size %dx%d, %d frames in 2s", w, h, len(got))
	if w > 1920 || w%2 != 0 || h%2 != 0 || w <= 0 || h <= 0 {
		t.Errorf("bad session size %dx%d", w, h)
	}
	if len(got) == 0 {
		t.Fatal("no frames received")
	}
	for i, f := range got {
		if f.w > 1920 || f.w%2 != 0 || f.h%2 != 0 {
			t.Errorf("frame %d: bad size %dx%d", i, f.w, f.h)
		}
		if f.format != pixelFormatNV12 {
			t.Errorf("frame %d: pixel format %#x, want NV12 %#x", i, f.format, pixelFormatNV12)
		}
		if i > 0 && f.at.Before(got[i-1].at) {
			t.Errorf("frame %d: CaptureAt %v before previous %v", i, f.at, got[i-1].at)
		}
		if d := time.Since(f.at); d < 0 || d > 10*time.Second {
			t.Errorf("frame %d: CaptureAt %v implausible (age %v)", i, f.at, d)
		}
	}
}

func TestCaptureWindowExclusionRejectsMissingWindow(t *testing.T) {
	if !HasPermission() {
		t.Skip("Screen Recording permission missing")
	}
	s, err := Start(Options{FPS: 30, MaxWidth: 320}, func(Frame) {})
	if errors.Is(err, errNoDisplay) {
		t.Skip("ScreenCaptureKit has no display in this login session")
	}
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := ExcludeWindows(s, []uint32{^uint32(0)}); err == nil {
		t.Fatal("capture accepted an unresolvable window exclusion")
	}
	if err := ExcludeWindows(s, nil); err != nil {
		t.Fatalf("restore default filter: %v", err)
	}
	if err := s.(*darwinSession).reopen(); err != nil {
		t.Fatalf("invalid exclusion was retained on reopen: %v", err)
	}
}

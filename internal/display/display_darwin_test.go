//go:build darwin && cgo

package display

import (
	"testing"
	"time"
)

// The test binary does not run AppKit on the main thread, so this exercises
// the layer and display link without the NSView (New is main-thread only).
func TestEnqueueLayer(t *testing.T) {
	v, err := newLayerOnly()
	if err != nil {
		t.Fatal(err)
	}
	defer v.Release()
	if v.Handle() != 0 {
		t.Fatal("layer-only view has an NSView")
	}
	const w, h = 1280, 720
	for i := 0; i < 30; i++ {
		pix, err := newTestNV12(w, h)
		if err != nil {
			t.Fatal(err)
		}
		before := time.Now()
		at, err := v.Enqueue(pix, before)
		releaseTestPixelBuffer(pix) // the layer keeps its own reference
		if err != nil {
			t.Fatalf("frame %d: %v", i, err)
		}
		if at.Before(before) {
			t.Fatalf("frame %d: enqueuedAt %v before call %v", i, at, before)
		}
		if s := v.status(); s == 2 {
			t.Fatalf("frame %d: layer status Failed", i)
		}
	}
	if fw, fh := v.FrameSize(); fw != w || fh != h {
		t.Fatalf("FrameSize = %dx%d, want %dx%d", fw, fh, w, h)
	}
	// A size change recreates the format description.
	pix, err := newTestNV12(640, 360)
	if err != nil {
		t.Fatal(err)
	}
	_, err = v.Enqueue(pix, time.Now())
	releaseTestPixelBuffer(pix)
	if err != nil {
		t.Fatal(err)
	}
	if fw, fh := v.FrameSize(); fw != 640 || fh != 360 {
		t.Fatalf("FrameSize after resize = %dx%d", fw, fh)
	}
	if _, err := v.Enqueue(0, time.Now()); err == nil {
		t.Fatal("nil pixel buffer accepted")
	}
}

func TestNextVsync(t *testing.T) {
	v, err := newLayerOnly()
	if err != nil {
		t.Fatal(err)
	}
	defer v.Release()
	// The display link needs a refresh or two before it reports a phase;
	// until then NextVsync returns after + period.
	deadline := time.Now().Add(time.Second)
	for {
		_, period, ok := v.vsyncInfo()
		if ok {
			if period < 2*time.Millisecond || period > 50*time.Millisecond {
				t.Fatalf("refresh period %v", period)
			}
			t.Logf("refresh period %v", period)
			break
		}
		if time.Now().After(deadline) {
			t.Log("display link reported no vsync within 1s; checking the fallback only")
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	for i := 0; i < 5; i++ {
		after := time.Now()
		next := v.NextVsync(after)
		if !next.After(after) || next.Sub(after) >= 50*time.Millisecond {
			t.Fatalf("NextVsync(%v) = +%v, want within (0, 50ms)", after, next.Sub(after))
		}
		// Later in the future: still the next tick after that instant.
		later := after.Add(123 * time.Millisecond)
		if n := v.NextVsync(later); n.Before(later) || n.Sub(later) >= 50*time.Millisecond {
			t.Fatalf("NextVsync(later) = +%v", n.Sub(later))
		}
		t.Logf("next vsync in %v", next.Sub(after))
		time.Sleep(7 * time.Millisecond)
	}
}

func TestReleaseIdempotent(t *testing.T) {
	v, err := newLayerOnly()
	if err != nil {
		t.Fatal(err)
	}
	v.Release()
	v.Release()
	if _, err := v.Enqueue(1, time.Now()); err == nil {
		t.Fatal("Enqueue after Release succeeded")
	}
	if fw, fh := v.FrameSize(); fw != 0 || fh != 0 {
		t.Fatalf("FrameSize = %dx%d", fw, fh)
	}
	_ = v.NextVsync(time.Now())
}

//go:build windows && cgo

package display

/*
#cgo CXXFLAGS: -std=c++17 -O2 -Wall -Wno-unknown-pragmas
#cgo windows LDFLAGS: -ld3d11 -ldxgi -ldwmapi -luser32 -lgdi32 -luuid -static-libgcc -static-libstdc++ -static
#include "display_windows.h"
*/
import "C"

import (
	"errors"
	"sync"
	"time"
	"unsafe"

	"github.com/tangoui/kkdesk/internal/winmedia"
)

//export mmdispwLog
func mmdispwLog(msg *C.char) { winmedia.Logf("%s", C.GoString(msg)) }

// fallbackPeriod is used when DWM gives no refresh period.
const fallbackPeriod = time.Second / 60

// View is a window showing NV12 frames through a flip-model swap chain,
// aspect fit with black bars. Tango's NativeHost parents it into the
// session window.
//
// Threading: New and Release must run on the UI thread (the window's
// thread, which runs the message loop). Enqueue, NextVsync and FrameSize
// may be called from any goroutine; calls are serialized internally.
type View struct {
	mu sync.Mutex // serializes the C calls; held while a frame is presented
	d  *C.mmDispW // nil after Release

	// sizeMu guards hwnd and the frame size, so the UI thread reading them
	// every frame never waits for a present on the decoder thread.
	sizeMu sync.Mutex
	hwnd   uintptr
	w, h   int
}

// New creates the view. It must be called on the UI thread.
func New() (*View, error) {
	var d *C.mmDispW
	var hwnd unsafe.Pointer
	if hr := int32(C.mmdispw_new(&d, &hwnd)); hr < 0 {
		return nil, winmedia.Check("display: creating the video window", hr)
	}
	return &View{d: d, hwnd: uintptr(hwnd)}, nil
}

// Handle returns the HWND (owned by the View until Release). Pass it to the
// UI toolkit to parent it. It is 0 after Release.
func (v *View) Handle() uintptr {
	v.sizeMu.Lock()
	defer v.sizeMu.Unlock()
	return v.hwnd
}

// Enqueue shows pix (an NV12 ID3D11Texture2D*, e.g. from the decoder
// callback) at once: it is copied (GPU) and presented, so pix only needs to
// be valid during the call. captureAt is informational.
func (v *View) Enqueue(pix uintptr, captureAt time.Time) (enqueuedAt time.Time, err error) {
	if pix == 0 {
		return time.Time{}, errors.New("display: nil texture")
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.d == nil {
		return time.Time{}, errors.New("display: view released")
	}
	var w, h C.int32_t
	hr := int32(C.mmdispw_enqueue(v.d, winmedia.Ptr(pix), &w, &h))
	enqueuedAt = time.Now()
	v.sizeMu.Lock()
	v.w, v.h = int(w), int(h)
	v.sizeMu.Unlock()
	if hr < 0 {
		return enqueuedAt, winmedia.Check("display: presenting the frame", hr)
	}
	return enqueuedAt, nil
}

// NextVsync returns the first refresh at or after `after`, extrapolated from
// DWM's last composition vblank and refresh period (the frame presented at
// `after` is composed at that refresh at the earliest). Without DWM timing
// it returns after + 1/60 s.
func (v *View) NextVsync(after time.Time) time.Time {
	var vblank, period, now, freq C.int64_t
	hr := int32(C.mmdispw_vsync(&vblank, &period, &now, &freq))
	t := time.Now()
	if hr < 0 || period <= 0 || freq <= 0 || vblank <= 0 {
		return after.Add(fallbackPeriod)
	}
	ticks := func(n C.int64_t) time.Duration {
		return time.Duration(float64(n) * float64(time.Second) / float64(freq))
	}
	ref := t.Add(-ticks(now - vblank))
	return nextTick(ref, ticks(period), after)
}

// FrameSize returns the size of the last enqueued frame (0, 0 before any).
func (v *View) FrameSize() (w, h int) {
	v.sizeMu.Lock()
	defer v.sizeMu.Unlock()
	return v.w, v.h
}

// renderTo draws the last frame into the BGRA texture target (tests).
func (v *View) renderTo(target uintptr) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.d == nil {
		return errors.New("display: view released")
	}
	return winmedia.Check("display: render", int32(C.mmdispw_render_to(v.d, winmedia.Ptr(target))))
}

// hold enters (true) or leaves the C-side lock without View.mu (tests).
func (v *View) hold(on bool) {
	n := C.int32_t(0)
	if on {
		n = 1
	}
	C.mmdispw_hold(v.d, n)
}

// presents returns the number of successful Present calls (tests).
func (v *View) presents() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.d == nil {
		return 0
	}
	return int(C.mmdispw_presents(v.d))
}

// Release destroys the window and the swap chain. It is idempotent and must
// be called on the UI thread.
func (v *View) Release() {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.d == nil {
		return
	}
	C.mmdispw_destroy(v.d)
	v.d = nil
	v.sizeMu.Lock()
	v.hwnd = 0
	v.sizeMu.Unlock()
}

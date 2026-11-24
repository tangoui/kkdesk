//go:build darwin && cgo

package display

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework AVFoundation -framework CoreMedia -framework CoreVideo -framework QuartzCore -framework AppKit -framework Foundation
#include "display_darwin.h"
*/
import "C"

import (
	"errors"
	"fmt"
	"sync"
	"time"
	"unsafe"
)

// View is a layer-hosting NSView whose layer is an AVSampleBufferDisplayLayer
// (aspect fit, black background).
//
// Threading: New and Release must run on the main thread (AppKit). Enqueue,
// NextVsync and FrameSize may be called from any goroutine; calls are
// serialized internally.
type View struct {
	mu   sync.Mutex
	d    unsafe.Pointer // MMDisplay*, nil after Release
	view uintptr
	w, h int
}

// New creates the view. It must be called on the main thread.
func New() (*View, error) { return newView(true) }

func newView(withView bool) (*View, error) {
	wv := C.int(0)
	if withView {
		wv = 1
	}
	d := C.mmdisp_new(wv)
	if d == nil {
		return nil, errors.New("display: creating view failed")
	}
	return &View{d: d, view: uintptr(C.mmdisp_view(d))}, nil
}

// Handle returns the NSView* (owned by the View until Release). Pass it to the
// UI toolkit to parent it. It is 0 after Release.
func (v *View) Handle() uintptr {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.view
}

// Enqueue shows pix (a CVPixelBufferRef, e.g. from the decoder callback) as
// soon as possible: it is wrapped in a CMSampleBuffer marked
// DisplayImmediately and enqueued on the layer, which retains it, so pix only
// needs to be valid during the call. captureAt is informational (callers log
// it next to enqueuedAt). If the layer is in the Failed state it is flushed
// and the frame retried once.
func (v *View) Enqueue(pix uintptr, captureAt time.Time) (enqueuedAt time.Time, err error) {
	if pix == 0 {
		return time.Time{}, errors.New("display: nil pixel buffer")
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.d == nil {
		return time.Time{}, errors.New("display: view released")
	}
	var w, h C.int32_t
	st := C.mmdisp_enqueue(v.d, ptrOf(pix), &w, &h)
	enqueuedAt = time.Now()
	v.w, v.h = int(w), int(h)
	if st == -1 {
		return enqueuedAt, errors.New("display: layer failed after flush")
	}
	if st != 0 {
		return enqueuedAt, fmt.Errorf("display: creating sample buffer failed: OSStatus %d", int32(st))
	}
	return enqueuedAt, nil
}

// fallbackPeriod is used when the display link gives no refresh period.
const fallbackPeriod = time.Second / 60

// NextVsync returns the first refresh of the main display at or after
// `after`, which is when a frame enqueued at `after` can appear at the
// earliest.
//
// Approximation: a CVDisplayLink for the main display (not necessarily the
// screen the view is on) runs while the View exists and records the predicted
// host time of every refresh; the result extrapolates from the latest one
// with the current refresh period. On variable-refresh (ProMotion) displays the real refresh
// may differ, and the compositor may show the frame one or more refreshes
// later. Before the first vsync is observed it returns after + period, and
// without a usable display link after + 1/60 s.
func (v *View) NextVsync(after time.Time) time.Time {
	v.mu.Lock()
	var until, period C.int64_t
	var hasPhase C.int32_t
	st := C.int32_t(-1)
	if v.d != nil {
		st = C.mmdisp_vsync(v.d, &until, &period, &hasPhase)
	}
	now := time.Now()
	v.mu.Unlock()
	if st != 0 || period <= 0 {
		return after.Add(fallbackPeriod)
	}
	if hasPhase == 0 { // no vsync observed yet (link just started)
		return after.Add(time.Duration(period))
	}
	return nextTick(now.Add(time.Duration(until)), time.Duration(period), after)
}

// vsyncInfo exposes the raw display link state for tests.
func (v *View) vsyncInfo() (ref time.Time, period time.Duration, hasPhase bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.d == nil {
		return time.Time{}, 0, false
	}
	var until, p C.int64_t
	var hp C.int32_t
	if C.mmdisp_vsync(v.d, &until, &p, &hp) != 0 {
		return time.Time{}, 0, false
	}
	return time.Now().Add(time.Duration(until)), time.Duration(p), hp != 0
}

// ptrOf converts a C pointer held in a uintptr (a CVPixelBufferRef) back to a
// pointer for passing to C. The referenced memory is never Go memory.
func ptrOf(p uintptr) unsafe.Pointer { return *(*unsafe.Pointer)(unsafe.Pointer(&p)) }

// FrameSize returns the size of the last enqueued frame (0, 0 before any).
func (v *View) FrameSize() (w, h int) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.w, v.h
}

// status returns the renderer status (0 unknown, 1 rendering, 2 failed).
func (v *View) status() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.d == nil {
		return 0
	}
	return int(C.mmdisp_status(v.d))
}

// Release removes the view from its superview if still attached, releases the
// view and layer and stops the display link. It is idempotent and must be
// called on the main thread (off the main thread the removal is dispatched
// asynchronously to the main queue).
func (v *View) Release() {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.d == nil {
		return
	}
	C.mmdisp_destroy(v.d)
	v.d = nil
	v.view = 0
}

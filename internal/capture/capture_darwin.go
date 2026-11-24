//go:build darwin && cgo

package capture

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework ScreenCaptureKit -framework CoreMedia -framework CoreVideo -framework CoreGraphics -framework Foundation
#include <stdlib.h>
#include "capture_darwin.h"
*/
import "C"

import (
	"errors"
	"runtime/cgo"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"
)

// HasPermission reports whether the process may record the screen
// (CGPreflightScreenCaptureAccess). It never shows a prompt.
func HasPermission() bool { return C.mmcap_has_permission() != 0 }

// RequestPermission asks the system for Screen Recording permission
// (CGRequestScreenCaptureAccess). The prompt is shown at most once per
// process; afterwards the user must enable it in System Settings.
func RequestPermission() { C.mmcap_request_permission() }

// pixelFormat returns the CVPixelBuffer's four-character pixel format code.
func pixelFormat(pixels uintptr) uint32 {
	return uint32(C.mmcap_pixel_format(C.uintptr_t(pixels)))
}

// pixelFormatNV12 is kCVPixelFormatType_420YpCbCr8BiPlanarVideoRange ('420v').
const pixelFormatNV12 = 0x34323076

type darwinSession struct {
	out    func(Frame)
	handle cgo.Handle
	w, h   int

	// wall/machNs map SCStreamFrameInfoDisplayTime (mach absolute time in
	// ns) to Go time: CaptureAt = wall + (displayNs - machNs).
	wall   time.Time
	machNs uint64

	mu     sync.Mutex
	ptr    unsafe.Pointer
	closed atomic.Bool

	complete, idle, other, late atomic.Uint64
}

// Start begins capturing the main display. out is called on a dedicated
// serial dispatch queue for every new (complete) frame; it must not block
// for long and must not call Close.
func Start(opts Options, out func(Frame)) (Session, error) {
	if out == nil {
		return nil, errors.New("capture: nil frame callback")
	}
	fps := opts.FPS
	if fps <= 0 {
		fps = 30
	}
	s := &darwinSession{out: out}
	s.machNs = uint64(C.mmcap_now_ns())
	s.wall = time.Now()
	s.handle = cgo.NewHandle(s)

	cExclude := C.CString(opts.ExcludeApp)
	defer C.free(unsafe.Pointer(cExclude))
	var cw, ch C.int
	var cerr *C.char
	ptr := C.mmcap_start(C.int64_t(minFrameInterval(fps)), C.int(opts.MaxWidth), cExclude,
		C.uintptr_t(s.handle), &cw, &ch, &cerr)
	if ptr == nil {
		s.closed.Store(true)
		s.handle.Delete()
		msg := "capture: start failed"
		if cerr != nil {
			msg = C.GoString(cerr)
			C.free(unsafe.Pointer(cerr))
		}
		return nil, errors.New(msg)
	}
	s.ptr = ptr
	s.w, s.h = int(cw), int(ch)
	return s, nil
}

func (s *darwinSession) SetFPS(fps int) {
	if fps <= 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ptr == nil {
		return
	}
	C.mmcap_set_fps(s.ptr, C.int64_t(minFrameInterval(fps)))
}

func (s *darwinSession) Size() (int, int) { return s.w, s.h }

func (s *darwinSession) Close() {
	s.closed.Store(true)
	s.mu.Lock()
	ptr := s.ptr
	s.ptr = nil
	s.mu.Unlock()
	if ptr == nil {
		return
	}
	C.mmcap_stop(ptr)
	s.handle.Delete()
}

//export mmcapGoFrame
func mmcapGoFrame(handle C.uintptr_t, pixels C.uintptr_t, displayNs, arrivalNs C.uint64_t, w, h C.int) {
	s, ok := cgo.Handle(handle).Value().(*darwinSession)
	if !ok || s.closed.Load() {
		return
	}
	wall := func(ns uint64) time.Time {
		if ns == 0 {
			return time.Time{}
		}
		return s.wall.Add(time.Duration(int64(ns - s.machNs)))
	}
	capNs, late := contentTime(uint64(displayNs), uint64(arrivalNs))
	if late {
		s.late.Add(1)
	}
	s.complete.Add(1)
	s.out(Frame{Pixels: uintptr(pixels), Width: int(w), Height: int(h),
		CaptureAt: wall(capNs), ArrivedAt: wall(uint64(arrivalNs)), DisplayAt: wall(uint64(displayNs))})
}

//export mmcapGoStatus
func mmcapGoStatus(handle C.uintptr_t, status C.int) {
	s, ok := cgo.Handle(handle).Value().(*darwinSession)
	if !ok || s.closed.Load() {
		return
	}
	switch status {
	case 1: // SCFrameStatusIdle
		s.idle.Add(1)
	default:
		s.other.Add(1)
	}
}

func (s *darwinSession) Stats() Stats {
	return Stats{Complete: s.complete.Load(), Idle: s.idle.Load(), Other: s.other.Load(), Late: s.late.Load()}
}

//export mmcapScaledSize
func mmcapScaledSize(w, h, maxW C.int, outW, outH *C.int) {
	sw, sh := scaledSize(int(w), int(h), int(maxW))
	*outW, *outH = C.int(sw), C.int(sh)
}

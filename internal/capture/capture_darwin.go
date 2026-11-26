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
	"slices"
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
	w, h   int // guarded by mu after Start (a restart may change them)

	// What the stream is started with, again on a restart.
	maxWidth   int
	excludeApp string
	interval   int64 // ns; guarded by mu
	rs         *restarter

	// wall/machNs map SCStreamFrameInfoDisplayTime (mach absolute time in
	// ns) to Go time: CaptureAt = wall + (displayNs - machNs).
	wall   time.Time
	machNs uint64

	streamMu sync.Mutex // serializes restart, filter changes and close
	excluded []uint32

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
	s := &darwinSession{out: out, maxWidth: opts.MaxWidth, excludeApp: opts.ExcludeApp,
		interval: int64(minFrameInterval(fps))}
	s.machNs = uint64(C.mmcap_now_ns())
	s.wall = time.Now()
	s.rs = &restarter{delays: []time.Duration{200 * time.Millisecond, time.Second, 2 * time.Second}, reopen: s.reopen}
	s.handle = cgo.NewHandle(s)

	ptr, w, h, err := s.start(s.interval)
	if err != nil {
		s.closed.Store(true)
		s.handle.Delete()
		return nil, err
	}
	s.ptr = ptr
	s.w, s.h = w, h
	return s, nil
}

// start starts a stream with the session's options.
func (s *darwinSession) start(interval int64) (unsafe.Pointer, int, int, error) {
	cExclude := C.CString(s.excludeApp)
	defer C.free(unsafe.Pointer(cExclude))
	var cw, ch C.int
	var cerr *C.char
	ptr := C.mmcap_start(C.int64_t(interval), C.int(s.maxWidth), cExclude, (*C.uint32_t)(unsafe.Pointer(unsafe.SliceData(s.excluded))), C.size_t(len(s.excluded)), C.uintptr_t(s.handle), &cw, &ch, &cerr)
	if ptr == nil {
		msg := "capture: start failed"
		if cerr != nil {
			msg = C.GoString(cerr)
			C.free(unsafe.Pointer(cerr))
		}
		return nil, 0, 0, errors.New(msg)
	}
	return ptr, int(cw), int(ch), nil
}

// reopen replaces a stream the system stopped with a new one (restarter).
func (s *darwinSession) reopen() error {
	s.streamMu.Lock()
	defer s.streamMu.Unlock()
	s.mu.Lock()
	old := s.ptr
	s.ptr = nil
	interval := s.interval
	s.mu.Unlock()
	if old != nil {
		C.mmcap_stop(old)
	}
	ptr, w, h, err := s.start(interval)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.ptr = ptr
	s.w, s.h = w, h
	if s.interval != interval {
		C.mmcap_set_fps(ptr, C.int64_t(s.interval))
	}
	s.mu.Unlock()
	return nil
}

func (s *darwinSession) excludeWindows(ids []uint32) error {
	s.streamMu.Lock()
	defer s.streamMu.Unlock()
	s.mu.Lock()
	ptr := s.ptr
	s.mu.Unlock()
	if s.closed.Load() || ptr == nil {
		if len(ids) == 0 {
			s.excluded = nil
			return nil
		}
		return errors.New("屏幕采集暂不可用")
	}
	app := C.CString(s.excludeApp)
	defer C.free(unsafe.Pointer(app))
	err := C.mmcap_exclude(ptr, app, (*C.uint32_t)(unsafe.Pointer(unsafe.SliceData(ids))), C.size_t(len(ids)))
	if err != nil {
		defer C.free(unsafe.Pointer(err))
		if len(ids) == 0 {
			// The covers are already hidden. Restart with the default filter.
			s.excluded = nil
			s.rs.trigger("restore capture filter")
		}
		return errors.New(C.GoString(err))
	}
	s.excluded = slices.Clone(ids)
	return nil
}

func (s *darwinSession) SetFPS(fps int) {
	if fps <= 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.interval = int64(minFrameInterval(fps))
	if s.ptr == nil {
		return
	}
	C.mmcap_set_fps(s.ptr, C.int64_t(s.interval))
}

func (s *darwinSession) Size() (int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w, s.h
}

func (s *darwinSession) Close() {
	if s.closed.Swap(true) {
		return
	}
	s.rs.close() // a running restart finishes first; its stream is stopped below
	s.streamMu.Lock()
	defer s.streamMu.Unlock()
	s.mu.Lock()
	ptr := s.ptr
	s.ptr = nil
	s.mu.Unlock()
	if ptr != nil { // nil when the last restart failed
		C.mmcap_stop(ptr)
	}
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

//export mmcapGoStopped
func mmcapGoStopped(handle C.uintptr_t, msg *C.char) {
	s, ok := cgo.Handle(handle).Value().(*darwinSession)
	if !ok || s.closed.Load() {
		return
	}
	s.rs.trigger(C.GoString(msg))
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
	return Stats{Complete: s.complete.Load(), Idle: s.idle.Load(), Other: s.other.Load(), Late: s.late.Load(),
		Reopened: s.rs.reopened()}
}

//export mmcapScaledSize
func mmcapScaledSize(w, h, maxW C.int, outW, outH *C.int) {
	sw, sh := scaledSize(int(w), int(h), int(maxW))
	*outW, *outH = C.int(sw), C.int(sh)
}

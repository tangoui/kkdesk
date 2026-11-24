//go:build windows && cgo

package capture

/*
#cgo CXXFLAGS: -std=c++17 -O2 -Wall -Wno-unknown-pragmas
#cgo windows LDFLAGS: -ld3d11 -ldxgi -lole32 -luuid -luser32 -lavrt -static-libgcc -static-libstdc++ -static
#include <stdlib.h>
#include "capture_windows.h"
*/
import "C"

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"mmdesk/internal/winmedia"
)

// ringSize is the number of NV12 output textures. The texture handed to
// out is not written again until two more frames were converted, and
// never while out runs (the loop is serial).
const ringSize = 3

// firstOpenTimeout bounds how long Start retries a failing duplication
// (for instance while a UAC prompt is showing) before giving up.
const firstOpenTimeout = 3 * time.Second

//export mmcapLog
func mmcapLog(msg *C.char) { winmedia.Logf("%s", C.GoString(msg)) }

// HasPermission reports true: Windows needs no screen recording permission
// (the secure desktop cannot be captured by a normal-privilege process,
// which is handled as a temporary loss).
func HasPermission() bool { return true }

// RequestPermission does nothing on Windows.
func RequestPermission() {}

type winSession struct {
	out  func(Frame)
	maxW int
	fps  atomic.Int64

	w, h      atomic.Int64
	closed    atomic.Bool
	done      chan struct{}
	ready     chan error
	closeOnce sync.Once

	// baseWall/baseNs map QPC ns to wall time.
	baseWall time.Time
	baseNs   uint64
	qpf      int64

	complete, idle, other, late, resent, reopened atomic.Uint64
	acquireNs                                     atomic.Int64
}

// dupState is owned by the capture goroutine.
type dupState struct {
	device  uintptr
	gen     uint64
	dup     *C.mmcapDup
	info    C.mmcapInfo
	conv    *winmedia.Converter
	convKey [4]int // in w, h, format, rotation the converter was built for
	ring    [ringSize]uintptr
	outW    int
	outH    int
	next    int
	last    int // ring index of the latest image, -1 when none
}

// Start begins capturing the primary display with DXGI Desktop
// Duplication. out is called on a dedicated OS thread for every new frame
// and, while the screen is unchanged, again with the last frame once per
// second; it must not block for long and must not call Close. The mouse
// pointer is not composited into the image.
func Start(opts Options, out func(Frame)) (Session, error) {
	if out == nil {
		return nil, errors.New("capture: nil frame callback")
	}
	fps := opts.FPS
	if fps <= 0 {
		fps = 30
	}
	s := &winSession{out: out, maxW: opts.MaxWidth, done: make(chan struct{}), ready: make(chan error, 1)}
	s.fps.Store(int64(fps))
	if opts.ExcludeApp != "" {
		winmedia.Logf("capture: ExcludeApp %q is not supported on Windows (ignored)", opts.ExcludeApp)
	}
	winmedia.Logf("capture: starting Desktop Duplication, %d fps, max width %d; the mouse pointer is not "+
		"composited (the viewer sees its own pointer)", fps, opts.MaxWidth)
	go s.run()
	select {
	case err := <-s.ready:
		if err != nil {
			<-s.done
			return nil, err
		}
	case <-time.After(firstOpenTimeout + 10*time.Second):
		s.Close()
		return nil, errors.New("capture: timed out starting Desktop Duplication")
	}
	return s, nil
}

func (s *winSession) SetFPS(fps int) {
	if fps > 0 {
		s.fps.Store(int64(fps))
	}
}

func (s *winSession) Size() (int, int) { return int(s.w.Load()), int(s.h.Load()) }

func (s *winSession) Stats() Stats {
	return Stats{Complete: s.complete.Load(), Idle: s.idle.Load(), Other: s.other.Load(), Late: s.late.Load(),
		Resent: s.resent.Load(), Reopened: s.reopened.Load(), AcquireTotal: time.Duration(s.acquireNs.Load())}
}

// Close stops the capture goroutine and waits for it; no out callback runs
// after Close returns.
func (s *winSession) Close() {
	s.closeOnce.Do(func() {
		s.closed.Store(true)
		<-s.done
		winmedia.Logf("capture: closed (complete=%d resent=%d idle=%d other=%d reopened=%d)",
			s.complete.Load(), s.resent.Load(), s.idle.Load(), s.other.Load(), s.reopened.Load())
	})
}

func (s *winSession) wall(qpc int64) time.Time {
	ns := qpcToNs(qpc, s.qpf)
	return s.baseWall.Add(time.Duration(int64(ns - s.baseNs)))
}

// sleep waits for d unless the session is closed first; it reports
// whether the session is still open.
func (s *winSession) sleep(d time.Duration) bool {
	for d > 0 && !s.closed.Load() {
		step := d
		if step > 50*time.Millisecond {
			step = 50 * time.Millisecond
		}
		time.Sleep(step)
		d -= step
	}
	return !s.closed.Load()
}

func (s *winSession) run() {
	defer close(s.done)
	// The thread keeps COM, MMCSS and DPI settings; it is never unlocked,
	// so it exits with this goroutine.
	runtime.LockOSThread()
	C.mmcap_thread_init()
	s.qpf = int64(C.mmcap_qpf())
	s.baseNs = qpcToNs(int64(C.mmcap_qpc()), s.qpf)
	s.baseWall = time.Now()

	st := &dupState{last: -1}
	defer st.closeAll()

	start := time.Now()
	var err error
	for attempt := 0; ; attempt++ {
		if err = s.open(st); err == nil || time.Since(start) > firstOpenTimeout || !s.sleep(reopenBackoff(attempt)) {
			break
		}
	}
	if err == nil && s.closed.Load() {
		err = errors.New("capture: closed during start")
	}
	if err != nil {
		s.ready <- err
		return
	}
	// Start returns once the first frame was converted (so a missing video
	// processor fails Start), or after firstOpenTimeout without one.
	readySent := false
	opened := time.Now()
	ready := func(err error) {
		if !readySent {
			readySent = true
			s.ready <- err
		}
	}
	defer ready(errors.New("capture: stopped during start"))

	var lastDeliver time.Time
	attempt := 0
	errLogs := 0
	for !s.closed.Load() {
		if !readySent && time.Since(opened) > firstOpenTimeout {
			winmedia.Logf("capture: no frame within %v of starting; continuing to wait", firstOpenTimeout)
			ready(nil)
		}
		if st.dup == nil {
			if !s.sleep(reopenBackoff(attempt)) {
				return
			}
			if err := s.open(st); err != nil {
				attempt++
				if attempt <= 3 || attempt%20 == 0 {
					winmedia.Logf("capture: re-creating duplication failed (attempt %d): %v", attempt, err)
				}
			} else {
				winmedia.Logf("capture: duplication re-created after %d failed attempt(s)", attempt)
				attempt = 0
				s.reopened.Add(1)
			}
			if st.last >= 0 && time.Since(lastDeliver) >= resendInterval {
				lastDeliver = s.deliver(st, st.last, time.Now(), time.Time{}, true)
			}
			continue
		}

		interval := minFrameInterval(int(s.fps.Load()))
		if wait := time.Until(lastDeliver.Add(interval)); wait > 0 {
			if !s.sleep(wait) {
				return
			}
		}
		timeout := interval
		if st.last >= 0 {
			if untilResend := time.Until(lastDeliver.Add(resendInterval)); untilResend < timeout {
				timeout = untilResend
			}
		}
		var f C.mmcapFrame
		r := int32(C.mmcap_acquire(st.dup, C.uint32_t(acquireTimeoutMs(timeout)), &f))
		switch {
		case r == C.MMCAP_FRAME:
			arrival := s.wall(int64(f.arrivalQpc))
			capNs, late := presentCaptureNs(qpcToNs(int64(f.presentQpc), s.qpf), qpcToNs(int64(f.arrivalQpc), s.qpf))
			if late {
				s.late.Add(1)
			}
			if err := s.convert(st, &f); err != nil {
				if !readySent {
					ready(fmt.Errorf("capture: converting the desktop image to NV12 failed: %w", err))
					return
				}
				s.other.Add(1)
				if errLogs++; errLogs <= 5 || errLogs%100 == 0 {
					winmedia.Logf("capture: convert: %v", err)
				}
				s.handleError(st, err)
				continue
			}
			s.acquireNs.Add(int64(float64(f.acquireMs) * float64(time.Millisecond)))
			capAt := s.baseWall.Add(time.Duration(int64(capNs - s.baseNs)))
			if s.complete.Load() == 0 {
				winmedia.Logf("capture: first frame %dx%d -> %dx%d, AcquireNextFrame %.2f ms, accumulated %d",
					int(f.width), int(f.height), st.outW, st.outH, float32(f.acquireMs), uint32(f.accumulated))
			}
			ready(nil)
			lastDeliver = s.deliver(st, st.last, capAt, arrival, false)
		case r == C.MMCAP_TIMEOUT || r == C.MMCAP_NOIMAGE:
			s.idle.Add(1)
			if st.last >= 0 && time.Since(lastDeliver) >= resendInterval {
				lastDeliver = s.deliver(st, st.last, time.Now(), time.Time{}, true)
			}
		default:
			s.other.Add(1)
			err := winmedia.Check("AcquireNextFrame", r)
			if r == winmedia.DXGIErrorAccessLost {
				winmedia.Logf("capture: %v (display mode change, UAC prompt, lock screen or full-screen app); "+
					"re-creating the duplication", err)
			} else if errLogs++; errLogs <= 5 || errLogs%100 == 0 {
				winmedia.Logf("capture: %v", err)
			}
			s.handleError(st, err)
		}
	}
}

// handleError drops the duplication (and, for a lost device, everything
// built on the device) so the loop re-creates it.
func (s *winSession) handleError(st *dupState, err error) {
	var he *winmedia.HRError
	if errors.As(err, &he) && (he.HR == winmedia.DXGIErrorDeviceRemoved || he.HR == winmedia.DXGIErrorDeviceReset) {
		winmedia.Logf("capture: D3D11 device lost; it will be re-created")
		st.closeAll()
		return
	}
	if st.dup != nil {
		C.mmcap_close(st.dup)
		st.dup = nil
	}
}

// open (re)creates the duplication; on a new device generation the
// converter and ring are dropped too.
func (s *winSession) open(st *dupState) error {
	dev, _, gen, err := winmedia.Device()
	if err != nil {
		return err
	}
	if st.device != 0 && st.gen != gen {
		st.closeAll()
	}
	st.device, st.gen = dev, gen
	if st.dup != nil {
		C.mmcap_close(st.dup)
		st.dup = nil
	}
	var d *C.mmcapDup
	var info C.mmcapInfo
	if hr := int32(C.mmcap_open(winmedia.Ptr(dev), &d, &info)); hr < 0 || d == nil {
		return winmedia.Check("DuplicateOutput", hr)
	}
	st.dup, st.info = d, info
	refresh := 0.0
	if info.refreshDen != 0 {
		refresh = float64(info.refreshNum) / float64(info.refreshDen)
	}
	winmedia.Logf("capture: duplicating primary output %dx%d @ %.2f Hz, rotation %d, format %d, system memory %d, "+
		"DuplicateOutput1 %d", int(info.width), int(info.height), refresh, int(info.rotation), int(info.format),
		int(info.inSystemMemory), int(info.duplicateOutput1))
	// Publish the expected output size now so Size() is right before the
	// first frame.
	if s.w.Load() == 0 {
		rw, rh := rotatedSize(int(info.width), int(info.height), int(info.rotation))
		ow, oh := scaledSize(rw, rh, s.maxW)
		s.w.Store(int64(ow))
		s.h.Store(int64(oh))
	}
	return nil
}

// convert scales the copied desktop image into the next ring texture,
// rebuilding the converter and ring when the image size, format or
// rotation changed.
func (s *winSession) convert(st *dupState, f *C.mmcapFrame) error {
	key := [4]int{int(f.width), int(f.height), int(f.format), int(st.info.rotation)}
	if st.conv == nil || st.convKey != key {
		st.closeConverter()
		rw, rh := rotatedSize(key[0], key[1], key[3])
		ow, oh := scaledSize(rw, rh, s.maxW)
		conv, err := winmedia.NewConverter(st.device, key[0], key[1], key[2], ow, oh, key[3])
		if err != nil {
			return err
		}
		st.conv, st.convKey, st.outW, st.outH = conv, key, ow, oh
		for i := range st.ring {
			t, err := winmedia.NewNV12(st.device, ow, oh)
			if err != nil {
				st.closeConverter()
				return err
			}
			st.ring[i] = t
		}
		st.next, st.last = 0, -1
		s.w.Store(int64(ow))
		s.h.Store(int64(oh))
		winmedia.Logf("capture: output %dx%d NV12 (desktop %dx%d, rotation %d)", ow, oh, rw, rh, key[3])
	}
	dst := st.ring[st.next]
	if err := st.conv.Convert(uintptr(f.texture), dst); err != nil {
		return err
	}
	st.last = st.next
	st.next = (st.next + 1) % ringSize
	return nil
}

// deliver calls out with ring texture idx and returns the delivery time.
func (s *winSession) deliver(st *dupState, idx int, capAt, arrival time.Time, resend bool) time.Time {
	if arrival.IsZero() {
		arrival = capAt
	}
	if resend {
		s.resent.Add(1)
	} else {
		s.complete.Add(1)
	}
	s.out(Frame{Pixels: st.ring[idx], Width: st.outW, Height: st.outH, CaptureAt: capAt, ArrivedAt: arrival})
	return time.Now()
}

func (st *dupState) closeConverter() {
	if st.conv != nil {
		st.conv.Close()
		st.conv = nil
	}
	for i, t := range st.ring {
		if t != 0 {
			winmedia.Release(t)
			st.ring[i] = 0
		}
	}
	st.last = -1
}

func (st *dupState) closeAll() {
	if st.dup != nil {
		C.mmcap_close(st.dup)
		st.dup = nil
	}
	st.closeConverter()
	st.device, st.gen = 0, 0
}

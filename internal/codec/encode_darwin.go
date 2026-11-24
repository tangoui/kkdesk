//go:build darwin && cgo

package codec

/*
#cgo darwin LDFLAGS: -framework VideoToolbox -framework CoreMedia -framework CoreVideo -framework CoreFoundation
#include "vt_darwin.h"
*/
import "C"

import (
	"fmt"
	"runtime/cgo"
	"sync"
	"time"
	"unsafe"
)

// vtEncoder is the VideoToolbox H.264 encoder.
type vtEncoder struct {
	mu     sync.Mutex
	enc    *C.mmEnc
	handle cgo.Handle
	out    EncodeOutput
	info   string

	errMu   sync.Mutex
	lastErr error
}

// NewEncoder creates a hardware H.264 encoder (VideoToolbox) for w×h NV12
// frames. Output is delivered to out on a VideoToolbox thread. The returned
// value also implements Info() string, describing the active mode.
func NewEncoder(w, h, fps, bps int, out EncodeOutput) (Encoder, error) {
	if w <= 0 || h <= 0 || fps <= 0 || bps <= 0 || out == nil {
		return nil, fmt.Errorf("codec: invalid encoder parameters %dx%d fps=%d bps=%d", w, h, fps, bps)
	}
	e := &vtEncoder{out: out}
	e.handle = cgo.NewHandle(e)
	var ce *C.mmEnc
	if st := C.mmEncCreate(C.int32_t(w), C.int32_t(h), C.int32_t(fps), C.int32_t(bps), C.uintptr_t(e.handle), &ce); st != 0 {
		e.handle.Delete()
		return nil, fmt.Errorf("codec: VTCompressionSessionCreate failed: OSStatus %d", int32(st))
	}
	e.enc = ce
	mode := "low-latency rate control"
	if C.mmEncLowLatency(ce) == 0 {
		mode = "standard rate control (low-latency unavailable)"
	}
	hw := "hardware requested (session does not report)"
	switch C.mmEncHardware(ce) {
	case 1:
		hw = "hardware"
	case 0:
		hw = "software"
	}
	e.info = fmt.Sprintf("VideoToolbox H.264 %s, %s, %dx%d", hw, mode, w, h)
	return e, nil
}

// Info describes the encoder mode (hardware/software, rate control).
func (e *vtEncoder) Info() string { return e.info }

func (e *vtEncoder) Encode(f Frame, forceKey bool) error {
	if f.Pixels == 0 {
		return fmt.Errorf("codec: nil pixel buffer")
	}
	e.errMu.Lock()
	err := e.lastErr
	e.lastErr = nil
	e.errMu.Unlock()
	if err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.enc == nil {
		return fmt.Errorf("codec: encoder closed")
	}
	fk := C.int32_t(0)
	if forceKey {
		fk = 1
	}
	// Pixels is a CVPixelBufferRef (a C pointer), not Go memory.
	pb := ptrOf(f.Pixels)
	if st := C.mmEncEncode(e.enc, pb, C.int64_t(f.CaptureAt.UnixNano()), fk); st != 0 {
		return fmt.Errorf("codec: VTCompressionSessionEncodeFrame failed: OSStatus %d", int32(st))
	}
	return nil
}

func (e *vtEncoder) SetBitrate(bps int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.enc != nil && bps > 0 {
		C.mmEncSetBitrate(e.enc, C.int32_t(bps))
	}
}

func (e *vtEncoder) SetFPS(fps int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.enc != nil && fps > 0 {
		C.mmEncSetFPS(e.enc, C.int32_t(fps))
	}
}

func (e *vtEncoder) Close() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.enc == nil {
		return
	}
	C.mmEncDestroy(e.enc) // flushes; no callbacks after this returns
	e.enc = nil
	e.handle.Delete()
}

// ptrOf converts a C object reference held as uintptr (e.g. a
// CVPixelBufferRef) back to a pointer for passing to C. The referenced memory
// is never Go memory.
func ptrOf(p uintptr) unsafe.Pointer { return *(*unsafe.Pointer)(unsafe.Pointer(&p)) }

//export mmEncOutput
func mmEncOutput(h C.uintptr_t, data unsafe.Pointer, n C.int, capNanos C.int64_t, key C.int, status C.int32_t) {
	e := cgo.Handle(h).Value().(*vtEncoder)
	if status != 0 {
		e.errMu.Lock()
		e.lastErr = fmt.Errorf("codec: encode failed: OSStatus %d", int32(status))
		e.errMu.Unlock()
		return
	}
	nal := unsafe.Slice((*byte)(data), int(n))
	e.out(nal, time.Unix(0, int64(capNanos)), key != 0)
}

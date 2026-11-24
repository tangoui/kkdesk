//go:build darwin && cgo

package codec

// Test helpers for codec_darwin_test.go: cgo is not allowed in _test.go
// files, so the CoreVideo pixel-buffer helpers live here (unexported).

/*
#include "vt_darwin.h"
*/
import "C"

import "fmt"

// newNV12PixelBuffer creates an IOSurface-backed NV12 video-range
// CVPixelBuffer (retained; release with releasePixelBuffer).
func newNV12PixelBuffer(w, h int) (uintptr, error) {
	var st C.int32_t
	p := C.mmTestNewNV12(C.int32_t(w), C.int32_t(h), &st)
	if st != 0 || p == nil {
		return 0, fmt.Errorf("CVPixelBufferCreate failed: %d", int32(st))
	}
	return uintptr(p), nil
}

// fillTestPattern fills Y with a diagonal gradient shifted by frame and UV with 128.
func fillTestPattern(p uintptr, frame int) { C.mmTestFill(ptrOf(p), C.int32_t(frame)) }

func releasePixelBuffer(p uintptr) { C.mmTestRelease(ptrOf(p)) }

func pixelBufferSize(p uintptr) (w, h int) {
	var cw, ch C.int32_t
	C.mmTestSize(ptrOf(p), &cw, &ch)
	return int(cw), int(ch)
}

//go:build darwin && cgo

package display

// Test helpers: cgo is not allowed in _test.go files.

/*
#include "display_darwin.h"
*/
import "C"

import (
	"fmt"
)

// newLayerOnly creates a View with only the display layer (no NSView), which
// is safe off the main thread.
func newLayerOnly() (*View, error) { return newView(false) }

// newTestNV12 returns a retained IOSurface-backed NV12 CVPixelBuffer.
func newTestNV12(w, h int) (uintptr, error) {
	var st C.int32_t
	p := C.mmdisp_test_nv12(C.int32_t(w), C.int32_t(h), &st)
	if st != 0 || p == nil {
		return 0, fmt.Errorf("CVPixelBufferCreate failed: %d", int32(st))
	}
	return uintptr(p), nil
}

func releaseTestPixelBuffer(p uintptr) { C.mmdisp_test_release(ptrOf(p)) }

// Package capture grabs the main display as NV12 CVPixelBuffers.
//
// On macOS it uses ScreenCaptureKit (macOS 12.3+). Other platforms return
// ErrUnsupported for now.
package capture

import (
	"errors"
	"math"
	"time"
)

// ErrUnsupported is returned by Start on platforms without a capture backend.
var ErrUnsupported = errors.New("capture: unsupported platform")

// Frame is one captured screen image.
type Frame struct {
	// Pixels is a CVPixelBufferRef (NV12, video range). It is only valid
	// during the out callback; call CVPixelBufferRetain to keep it longer.
	Pixels uintptr
	// CaptureAt is the time the frame was displayed on screen, taken from
	// the sample's SCStreamFrameInfoDisplayTime (mach absolute time) and
	// mapped to wall clock through an offset captured at Start. It falls
	// back to time.Now() at callback entry when the attachment is missing.
	CaptureAt time.Time
	// Width and Height are the pixel buffer dimensions.
	Width, Height int
}

// Options configures a capture session.
type Options struct {
	// FPS is the maximum frame rate (minimumFrameInterval = 1/FPS).
	// Values <= 0 mean 30.
	FPS int
	// MaxWidth scales the output down (keeping aspect) so that its width
	// does not exceed MaxWidth. Values <= 0 mean no limit.
	MaxWidth int
	// ExcludeApp hides the windows of every application whose name or
	// bundle executable name equals this value (case-insensitive).
	ExcludeApp string
}

// Session is a running capture.
type Session interface {
	// SetFPS changes the maximum frame rate of the running stream.
	SetFPS(fps int)
	// Size returns the output frame size.
	Size() (w, h int)
	// Close stops the stream and releases its resources. It is idempotent
	// and no out callback runs after it returns.
	Close()
}

// scaledSize returns the output size for a w×h display limited to maxW
// pixels wide, keeping aspect ratio; both results are rounded to even.
func scaledSize(w, h, maxW int) (int, int) {
	if maxW > 0 && w > maxW {
		h = int(math.Round(float64(h)*float64(maxW)/float64(w)/2)) * 2
		w = maxW
	}
	w &^= 1
	h &^= 1
	if w < 2 {
		w = 2
	}
	if h < 2 {
		h = 2
	}
	return w, h
}

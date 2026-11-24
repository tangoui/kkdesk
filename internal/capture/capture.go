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
	// CaptureAt is the capture time used for latency measurement: the
	// earlier of the frame's SCK display time and the moment the capture
	// callback received it (ArrivedAt), so it never lies in the future of
	// the moment we hold the frame. In practice SCK delivers a frame
	// 3–10 ms before the vsync it stamps as SCStreamFrameInfoDisplayTime
	// (the PTS is the same instant), so CaptureAt is usually ArrivedAt.
	// Both clocks are mach absolute time (verified equal to Go's monotonic
	// clock), mapped to wall time through an offset taken at Start.
	CaptureAt time.Time
	// ArrivedAt is when the capture callback was entered.
	ArrivedAt time.Time
	// DisplayAt is SCStreamFrameInfoDisplayTime (zero when missing): the
	// vsync the composited frame is shown at locally. The encoder cannot
	// finish before roughly this time (the surface is still being
	// composited), so encode completion is measured ~6–8 ms after it.
	DisplayAt time.Time
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
	// Stats returns the frame counters since Start.
	Stats() Stats
	// Close stops the stream and releases its resources. It is idempotent
	// and no out callback runs after it returns.
	Close()
}

// contentTime returns CaptureAt in mach ns from the SCK display time
// (0 when missing) and the callback arrival time; late reports a display
// time after arrival, which then falls back to arrival.
func contentTime(displayNs, arrivalNs uint64) (ns uint64, late bool) {
	if displayNs == 0 {
		return arrivalNs, false
	}
	if displayNs > arrivalNs {
		return arrivalNs, true
	}
	return displayNs, false
}

// minFrameInterval is the SCK minimumFrameInterval for a fps cap: 10%
// below 1/fps, because SCK measures against vsync timestamps that jitter
// around the refresh period, and an exact 1/60 on a 60 Hz panel skipped
// vsyncs (57 fps). 10% is far less than one refresh period, so lower caps
// still land on every n-th vsync.
func minFrameInterval(fps int) time.Duration {
	if fps <= 0 {
		fps = 30
	}
	return time.Duration(float64(time.Second) * 0.9 / float64(fps))
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

// Stats counts ScreenCaptureKit callbacks by frame status.
type Stats struct {
	Complete uint64 // frames with a new image (delivered to out)
	Idle     uint64 // SCFrameStatusIdle: screen unchanged, no image
	Other    uint64 // blank/suspended/started/stopped or malformed samples
	// Late counts complete frames whose SCK display time was after the
	// callback arrival (CaptureAt then falls back to the arrival time).
	Late uint64
}

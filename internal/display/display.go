// Package display shows decoded video frames in a native view (macOS: an
// AVSampleBufferDisplayLayer; Windows: a window with a flip-model swap chain
// fed by a D3D11 video processor), and maps pointer positions in the view to
// normalized video coordinates.
package display

import (
	"errors"
	"time"
)

// ErrUnsupported is returned by New on platforms without a display backend.
var ErrUnsupported = errors.New("display: unsupported platform")

// VideoRect returns the aspect-fit rectangle of a frameW×frameH video inside a
// boxW×boxH box (same units as the box, origin at the box's top-left corner).
// The video is centered with black bars on the top/bottom (letterbox) or the
// left/right (pillarbox). All results are zero when any size is not positive.
func VideoRect(boxW, boxH float32, frameW, frameH int) (x, y, w, h float32) {
	if boxW <= 0 || boxH <= 0 || frameW <= 0 || frameH <= 0 {
		return 0, 0, 0, 0
	}
	fw, fh := float64(frameW), float64(frameH)
	bw, bh := float64(boxW), float64(boxH)
	s := min(bw/fw, bh/fh)
	vw, vh := fw*s, fh*s
	return float32((bw - vw) / 2), float32((bh - vh) / 2), float32(vw), float32(vh)
}

// Normalize converts a point (px, py) in box coordinates (origin top-left) to
// normalized video coordinates in [0,1] (origin top-left of the video). The
// result is clamped to [0,1]; inside is false when the point lies outside the
// video area (in the black bars or outside the box) or a size is not positive.
func Normalize(px, py float32, boxW, boxH float32, frameW, frameH int) (nx, ny float32, inside bool) {
	x, y, w, h := VideoRect(boxW, boxH, frameW, frameH)
	if w <= 0 || h <= 0 {
		return 0, 0, false
	}
	fx := (float64(px) - float64(x)) / float64(w)
	fy := (float64(py) - float64(y)) / float64(h)
	inside = fx >= 0 && fx <= 1 && fy >= 0 && fy <= 1
	return float32(clamp01(fx)), float32(clamp01(fy)), inside
}

func clamp01(v float64) float64 { return max(0, min(1, v)) }

// nextTick returns the first time ref + k·period (k any integer, period > 0)
// that is at or after `after`.
func nextTick(ref time.Time, period time.Duration, after time.Time) time.Time {
	d := after.Sub(ref)
	k := d / period // truncates toward zero, which is the ceiling for d < 0
	if d > 0 && d > k*period {
		k++
	}
	return ref.Add(k * period)
}

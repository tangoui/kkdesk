package capture

import "time"

// This file holds the platform-independent parts of the Windows Desktop
// Duplication backend (capture_windows.go) so they can be unit-tested on
// every platform.

// DXGI_MODE_ROTATION values reported in DXGI_OUTDUPL_DESC.Rotation.
const (
	dxgiRotationIdentity = 1
	dxgiRotation90       = 2
	dxgiRotation180      = 3
	dxgiRotation270      = 4
)

// resendInterval is how often the last frame is delivered again while the
// screen is unchanged (or duplication is lost), so that a viewer that
// joined late or lost packets still gets a picture.
const resendInterval = time.Second

// reopenBackoff is the wait before the attempt-th (0-based) retry of
// re-creating a lost duplication: 100 ms doubling, at most 2 s.
func reopenBackoff(attempt int) time.Duration {
	d := 100 * time.Millisecond
	for i := 0; i < attempt && d < 2*time.Second; i++ {
		d *= 2
	}
	if d > 2*time.Second {
		d = 2 * time.Second
	}
	return d
}

// rotatedSize returns the desktop size as the user sees it for a desktop
// image of w×h delivered in the panel's native orientation.
func rotatedSize(w, h, rotation int) (int, int) {
	if rotation == dxgiRotation90 || rotation == dxgiRotation270 {
		return h, w
	}
	return w, h
}

// acquireTimeoutMs converts the frame interval to the AcquireNextFrame
// timeout in milliseconds, rounded up, at least 1 and at most 1000 (the
// capture loop checks for Close between calls).
func acquireTimeoutMs(d time.Duration) uint32 {
	ms := (d + time.Millisecond - 1) / time.Millisecond
	if ms < 1 {
		return 1
	}
	if ms > 1000 {
		return 1000
	}
	return uint32(ms)
}

// qpcToNs converts a QueryPerformanceCounter value to nanoseconds without
// overflowing for large counters.
func qpcToNs(qpc, freq int64) uint64 {
	if freq <= 0 || qpc <= 0 {
		return 0
	}
	sec := qpc / freq
	rem := qpc % freq
	return uint64(sec)*1_000_000_000 + uint64(rem*1_000_000_000/freq)
}

// maxPresentAge bounds how far before the AcquireNextFrame return a
// frame's LastPresentTime may lie and still be used as its capture time.
// The first frame after (re)creating a duplication carries the time of the
// last desktop change, which may be minutes old.
const maxPresentAge = 250 * time.Millisecond

// presentCaptureNs returns the capture time (QPC ns) of a duplicated frame
// from its LastPresentTime and the AcquireNextFrame return time: the
// present time when plausible, else the arrival. late reports a present
// time after arrival (clock disagreement), as contentTime does.
func presentCaptureNs(presentNs, arrivalNs uint64) (ns uint64, late bool) {
	if presentNs != 0 && presentNs < arrivalNs && arrivalNs-presentNs > uint64(maxPresentAge) {
		return arrivalNs, false
	}
	return contentTime(presentNs, arrivalNs)
}

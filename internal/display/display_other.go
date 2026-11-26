//go:build !(darwin || windows) || !cgo

package display

import "time"

// View is not implemented on this platform.
type View struct{}

// New returns ErrUnsupported on this platform.
func New() (*View, error) { return nil, ErrUnsupported }

// Handle returns 0 on this platform.
func (v *View) Handle() uintptr { return 0 }

// Enqueue returns ErrUnsupported on this platform.
func (v *View) Enqueue(pix uintptr, captureAt time.Time) (time.Time, error) {
	return time.Time{}, ErrUnsupported
}

// NextVsync returns after + 1/60 s on this platform.
func (v *View) NextVsync(after time.Time) time.Time { return after.Add(time.Second / 60) }

// FrameSize returns 0, 0 on this platform.
func (v *View) FrameSize() (w, h int) { return 0, 0 }

// Release does nothing on this platform.
func (v *View) Release() {}

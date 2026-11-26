// Package privacy provides the native operations for a host desktop session.
package privacy

import "errors"

var ErrUnsupported = errors.New("当前平台不支持隐私屏")

// Screen covers the local displays while letting input through. Show and
// Hide run off the GUI thread. The optional capture callback changes the
// window exclusions before a new macOS cover becomes visible. exited
// reports the local shortcut (nil) or a platform failure asynchronously.
type Screen interface {
	Show(exclude func([]uint32) error, exited func(error)) error
	Hide()
}

type unsupportedScreen struct{}

func (unsupportedScreen) Show(func([]uint32) error, func(error)) error { return ErrUnsupported }
func (unsupportedScreen) Hide()                                        {}

// Locker returns success after the operating system confirms the lock.
type Locker interface {
	Lock() error
}

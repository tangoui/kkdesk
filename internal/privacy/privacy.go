// Package privacy provides the native operations for a host desktop session.
package privacy

import (
	"errors"
	"time"
)

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

var errLockUnsupported = errors.New("当前平台不支持锁屏")

var errLockTimeout = errors.New("等待系统确认锁屏超时（5 秒）")

type confirmedLocker struct {
	request func() error
	locked  func() (bool, error)
}

func (l confirmedLocker) Lock() error {
	deadline := time.Now().Add(5 * time.Second)
	if err := l.request(); err != nil {
		return err
	}
	for {
		ok, err := l.locked()
		if err != nil {
			return err
		}
		if ok && !time.Now().After(deadline) {
			return nil
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return errLockTimeout
		}
		time.Sleep(min(remaining, 100*time.Millisecond))
	}
}

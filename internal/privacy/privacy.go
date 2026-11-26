// Package privacy provides the native operations for a host desktop session.
package privacy

import (
	"context"
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
	SetLockCountdown(seconds int)
}

type unsupportedScreen struct{}

func (unsupportedScreen) SetLockCountdown(int) {}

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
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- l.confirm(ctx) }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return errLockTimeout
	}
}

func (l confirmedLocker) confirm(ctx context.Context) error {
	if err := l.request(); err != nil {
		return err
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return errLockTimeout
		}
		ok, err := l.locked()
		if err != nil {
			return err
		}
		if ok && ctx.Err() == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return errLockTimeout
		case <-ticker.C:
		}
	}
}

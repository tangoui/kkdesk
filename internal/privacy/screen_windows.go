//go:build windows && cgo

package privacy

/*
#cgo CXXFLAGS: -std=c++17 -O2 -Wall
#cgo LDFLAGS: -luser32 -lgdi32 -lwtsapi32 -static-libgcc -static-libstdc++
#include "privacy_windows.h"
*/
import "C"

import (
	"errors"
	"fmt"
	"runtime"
	"syscall"

	"golang.org/x/sys/windows"
)

type windowsScreen struct {
	build  uint32
	open   func() error
	window C.uintptr_t
	done   chan struct{}
	exited func(error)
}

func NewScreen() Screen {
	screen := &windowsScreen{build: windows.RtlGetVersion().BuildNumber}
	screen.open = screen.start
	return screen
}

func (s *windowsScreen) Show(_ func([]uint32) error, exited func(error)) error {
	if s.build < 19041 {
		return errors.New("隐私屏需要 Windows 10 2004（build 19041）或更新版本")
	}
	if s.done != nil {
		return nil
	}
	s.exited = exited
	return s.open()
}

func (s *windowsScreen) start() error {
	type result struct {
		window C.uintptr_t
		err    error
	}
	started := make(chan result, 1)
	done := make(chan struct{})
	exited := s.exited
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		var window C.uintptr_t
		var code C.ulong
		ptr := C.mmprivacyw_open(&window, &code)
		if ptr == nil {
			started <- result{err: fmt.Errorf("无法创建可排除采集的隐私遮罩或注册退出快捷键：%w", syscall.Errno(code))}
			return
		}
		started <- result{window: window}
		code = C.mmprivacyw_run(ptr)
		close(done)
		switch code {
		case 0:
		case C.ulong(windows.ERROR_CANCELLED):
			exited(nil)
		default:
			exited(fmt.Errorf("隐私遮罩已停止：%w", syscall.Errno(code)))
		}
	}()
	r := <-started
	if r.err != nil {
		return r.err
	}
	s.window, s.done = r.window, done
	return nil
}

func (s *windowsScreen) Hide() {
	if s.done == nil {
		return
	}
	select {
	case <-s.done:
	default:
		C.mmprivacyw_stop(s.window)
	}
	<-s.done
	s.window, s.done = 0, nil
}

func (s *windowsScreen) SetLockCountdown(seconds int) {
	if s.window != 0 {
		C.mmprivacyw_countdown(s.window, C.int(seconds))
	}
}

func NewLocker() Locker {
	return confirmedLocker{
		request: func() error {
			if code := C.mmprivacyw_lock(); code != 0 {
				return fmt.Errorf("Windows 拒绝锁屏请求：%w", syscall.Errno(code))
			}
			return nil
		},
		locked: func() (bool, error) {
			var locked C.int
			if code := C.mmprivacyw_locked(&locked); code != 0 {
				return false, fmt.Errorf("无法读取 Windows 当前会话的锁屏状态：%w", syscall.Errno(code))
			}
			return locked != 0, nil
		},
	}
}

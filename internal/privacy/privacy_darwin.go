//go:build darwin && cgo

package privacy

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework AppKit -framework Carbon
#include <stdlib.h>
#include "privacy_darwin.h"
*/
import "C"

import (
	"errors"
	"fmt"
	"runtime/cgo"
	"sync"
	"unsafe"
)

func NewScreen() Screen { return &darwinScreen{} }

type darwinScreen struct {
	mu      sync.Mutex
	ptr     unsafe.Pointer
	handle  cgo.Handle
	exclude func([]uint32) error
	exited  func(error)
}

func (s *darwinScreen) Show(exclude func([]uint32) error, exited func(error)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if exclude == nil {
		return errors.New("无法更新屏幕采集过滤器")
	}
	if s.ptr != nil {
		return nil
	}
	s.handle = cgo.NewHandle(s)
	var status C.int
	s.ptr = C.mmprivacy_new(C.uintptr_t(s.handle), &status)
	if s.ptr == nil {
		s.handle.Delete()
		return fmt.Errorf("无法注册隐私屏退出快捷键（%d）", status)
	}
	s.exclude, s.exited = exclude, exited
	if err := s.refresh(); err != nil {
		s.close()
		return err
	}
	return nil
}

func (s *darwinScreen) refresh() error {
	var count C.size_t
	ids := C.mmprivacy_prepare(s.ptr, &count)
	if ids == nil {
		return errors.New("无法创建隐私遮罩窗口")
	}
	defer C.free(unsafe.Pointer(ids))
	if err := s.exclude(unsafe.Slice((*uint32)(unsafe.Pointer(ids)), int(count))); err != nil {
		return err
	}
	C.mmprivacy_show(s.ptr)
	return nil
}

func (s *darwinScreen) close() {
	C.mmprivacy_close(s.ptr)
	s.ptr = nil
	s.handle.Delete()
	_ = s.exclude(nil)
	s.exclude, s.exited = nil, nil
}

func (s *darwinScreen) Hide() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ptr != nil {
		s.close()
	}
}

//export mmprivacyDarwinEvent
func mmprivacyDarwinEvent(handle C.uintptr_t, displayChanged C.int) {
	h := cgo.Handle(handle)
	s := h.Value().(*darwinScreen)
	go func() {
		s.mu.Lock()
		if s.ptr == nil || s.handle != h {
			s.mu.Unlock()
			return
		}
		exited := s.exited
		var err error
		if displayChanged != 0 {
			err = s.refresh()
			if err == nil {
				s.mu.Unlock()
				return
			}
		}
		s.mu.Unlock()
		exited(err)
	}()
}

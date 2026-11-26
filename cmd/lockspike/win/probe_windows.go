//go:build windows && amd64 && cgo

package main

/*
#cgo windows LDFLAGS: -ld3d11 -ldxgi -lwtsapi32 -luser32 -static-libgcc -static-libstdc++ -static
#include <stdlib.h>
#include "probe_windows.h"
*/
import "C"

import (
	"errors"
	"fmt"
	"image"
	"image/png"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const lockedState = 0 // WTS_SESSIONSTATE_LOCK on Windows 10 and 11

func lockState(session uint32) (int, uint32) {
	var code C.uint32_t
	state := C.spike_lock_state(C.uint32_t(session), &code)
	return int(state), uint32(code)
}

func probe(dir string, logger *log.Logger) error {
	// The desktop association applies to the native thread for all DXGI and input calls.
	runtime.LockOSThread()
	var session uint32
	if err := windows.ProcessIdToSessionId(uint32(os.Getpid()), &session); err != nil {
		return fmt.Errorf("ProcessIdToSessionId: %w", err)
	}
	var code C.uint32_t
	desk := C.spike_enter_desktop(C.uint32_t(session), &code)
	if desk == 0 {
		return fmt.Errorf("enter lock desktop: session=%d error=%d", session, code)
	}
	defer C.spike_leave_desktop(desk)
	logger.Printf("LocalSystem helper session=%d inputDesktop=Winlogon", session)

	before := capture(dir, "before", session, logger)
	logger.Printf("before result: %v", before)
	code = 0
	if C.spike_secure_desktop(C.uint32_t(session), &code) == 0 {
		return fmt.Errorf("lock desktop changed before input: %d", code)
	}
	sent := C.spike_post(&code)
	logger.Printf("SendInput sent=%d/6 error=%d (confirm password dots visually)", sent, code)
	var inputErr error
	if sent != 6 {
		inputErr = fmt.Errorf("SendInput sent %d events, error=%d", sent, code)
	}
	time.Sleep(time.Second)
	after := capture(dir, "after", session, logger)
	logger.Printf("after result: %v", after)
	return errors.Join(before, inputErr, after)
}

func capture(dir, name string, session uint32, logger *log.Logger) error {
	var code C.uint32_t
	if C.spike_secure_desktop(C.uint32_t(session), &code) == 0 {
		return fmt.Errorf("lock desktop changed before DXGI: %d", code)
	}
	var pixels *C.uint8_t
	var width, height C.uint32_t
	var stage *C.char
	hr := C.spike_capture(&pixels, &width, &height, &stage)
	if hr < 0 {
		return fmt.Errorf("DXGI %s HRESULT=0x%08X", C.GoString(stage), uint32(hr))
	}
	defer C.free(unsafe.Pointer(pixels))
	code = 0
	if C.spike_secure_desktop(C.uint32_t(session), &code) == 0 {
		return fmt.Errorf("lock desktop changed after DXGI: %d", code)
	}
	w, h := int(width), int(height)
	frame := &image.NRGBA{
		Pix:    unsafe.Slice((*byte)(unsafe.Pointer(pixels)), w*h*4),
		Stride: w * 4,
		Rect:   image.Rect(0, 0, w, h),
	}
	path := filepath.Join(dir, name+".png")
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	encodeErr := png.Encode(file, frame)
	if err := errors.Join(encodeErr, file.Close()); err != nil {
		return err
	}
	logger.Printf("DXGI %s saved %dx%d", filepath.Base(path), w, h)
	return nil
}

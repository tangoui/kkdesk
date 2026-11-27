//go:build windows && amd64 && cgo

package lockscreen

/*
#cgo CXXFLAGS: -std=c++17 -O2 -Wall
#cgo LDFLAGS: -ld3d11 -ldxgi -luser32 -lwtsapi32 -static-libgcc -static-libstdc++ -static
#include "desktop_windows.h"
*/
import "C"

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"log"
	"os"
	"runtime"
	"time"
	"unicode/utf16"
	"unicode/utf8"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/tangoui/kkdesk/internal/inject"
	"github.com/tangoui/kkdesk/internal/input"
	"github.com/tangoui/kkdesk/internal/winmedia"
)

func windowsSessionLocked(session uint32) bool { return C.kklock_state(C.uint32_t(session)) == 0 }

func runHelper(exe string, servicePID uint32, sasName string, logger *log.Logger) error {
	runtime.LockOSThread()
	service, err := windows.OpenProcess(windows.SYNCHRONIZE, false, servicePID)
	if err != nil {
		return err
	}
	go func() {
		defer windows.CloseHandle(service)
		_, _ = windows.WaitForSingleObject(service, windows.INFINITE)
		os.Exit(0)
	}()
	var session uint32
	if err := windows.ProcessIdToSessionId(uint32(os.Getpid()), &session); err != nil {
		return err
	}
	sid, err := consoleUser(session)
	if err != nil {
		return err
	}
	want := localIdentity{session: session, sid: sid, image: exe}
	valid := func() bool {
		if !allowLockedDesktop(session, windows.WTSGetActiveConsoleSessionId(), windowsSessionLocked(session), "Winlogon") {
			return false
		}
		if current, err := consoleUser(session); err != nil || current != sid {
			return false
		}
		desk := C.kklock_open_desktop()
		if desk == 0 {
			return false
		}
		defer C.kklock_close_desktop(desk)
		return C.kklock_is_winlogon(desk) != 0
	}
	if !valid() {
		return ErrUnavailable
	}
	desk := C.kklock_open_desktop()
	if desk == 0 {
		return ErrUnavailable
	}
	defer C.kklock_close_desktop(desk)
	if C.kklock_is_winlogon(desk) == 0 || C.kklock_enter_desktop(desk) == 0 {
		return ErrUnavailable
	}
	sasEvent, err := windows.OpenEvent(windows.EVENT_MODIFY_STATE, false, sasEventName(sasName))
	if err != nil {
		return err
	}
	defer windows.CloseHandle(sasEvent)
	sas := func() bool {
		if !valid() || !sasAllowed() {
			logger.Print("SAS unavailable: locked console or Windows service policy required")
			return false
		}
		return windows.SetEvent(sasEvent) == nil
	}
	for valid() {
		if err := acceptClient(want, valid, sas, logger); err != nil {
			return err
		}
	}
	return nil
}

func acceptClient(want localIdentity, valid, sas func() bool, logger *log.Logger) error {
	pipe, err := pipeListener(want.session, want.sid)
	if err != nil {
		return err
	}
	if err := waitForClient(pipe, valid); err != nil {
		_ = windows.CloseHandle(pipe)
		return err
	}
	file := os.NewFile(uintptr(pipe), "kkdesk lock screen")
	defer file.Close()
	var client localIdentity
	proc, err := pipeClient(pipe, &client)
	if err == nil {
		defer windows.CloseHandle(proc)
	}
	currentUser, userErr := consoleUser(want.session)
	allowed := err == nil && userErr == nil && currentUser == want.sid && client.matches(want) && valid()
	logger.Printf("ipc pid=%d image=%q sid=%q session=%d allowed=%t", client.pid, client.image, client.sid, client.session, allowed)
	if !allowed {
		return nil
	}
	active := func() bool { return valid() && pipeConnected(pipe) }
	if err := serveClient(file, proc, active, sas); err != nil && active() &&
		!errors.Is(err, io.EOF) && !errors.Is(err, windows.ERROR_BROKEN_PIPE) {
		logger.Printf("ipc pid=%d session ended: %v", client.pid, err)
	}
	return nil
}

func serveClient(file *os.File, proc windows.Handle, valid, sas func() bool) error {
	fileMap, err := windows.CreateFileMapping(windows.InvalidHandle, nil, windows.PAGE_READWRITE, 0, 2*frameSlotBytes, nil)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(fileMap)
	address, err := windows.MapViewOfFile(fileMap, windows.FILE_MAP_WRITE, 0, 0, 2*frameSlotBytes)
	if err != nil {
		return err
	}
	defer windows.UnmapViewOfFile(address)
	in, err := inject.New()
	if err != nil {
		return err
	}
	var native *C.kklock_capture
	if hr := int32(C.kklock_open_capture(&native)); hr < 0 {
		return winmedia.Check("lock screen DuplicateOutput", hr)
	}
	defer C.kklock_close_capture(native)
	var clientMap windows.Handle
	if err := windows.DuplicateHandle(windows.CurrentProcess(), fileMap, proc, &clientMap, windows.FILE_MAP_READ, false, 0); err != nil {
		return err
	}
	if err := file.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		return err
	}
	if err := binary.Write(file, binary.LittleEndian, uint64(clientMap)); err != nil {
		return err
	}
	var held input.Held
	defer func() {
		if valid() {
			for _, e := range held.Release() {
				_ = in.Apply(e)
			}
		}
	}()
	var sequence uint64
	var payload [maxSecretBytes]byte
	defer clear(payload[:])
	frame := make([]byte, frameSlotBytes)
	var frameReply [21]byte
	for valid() {
		if err := file.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
			return err
		}
		op, data, err := receiveCommand(file, payload[:])
		if err != nil {
			return err
		}
		if !valid() {
			return ErrUnavailable
		}
		switch op {
		case ipcFrame:
			if len(data) != 0 {
				return ErrUnavailable
			}
			var width, height, rotation C.uint32_t
			hr := int32(C.kklock_grab(native, unsafe.Pointer(&frame[0]), frameSlotBytes, &width, &height, &rotation))
			if hr < 0 {
				return winmedia.Check("lock screen AcquireNextFrame", hr)
			}
			frameReply[0] = replyIdle
			if hr == 0 && valid() {
				sequence++
				offset, size, ok := frameBounds(uint32(width), uint32(height), sequence)
				if !ok {
					return ErrUnavailable
				}
				copy(unsafe.Slice((*byte)(winmedia.Ptr(address+uintptr(offset))), size), frame[:size])
				frameReply[0] = replyOK
				binary.LittleEndian.PutUint64(frameReply[1:], sequence)
				binary.LittleEndian.PutUint32(frameReply[9:], uint32(width))
				binary.LittleEndian.PutUint32(frameReply[13:], uint32(height))
				binary.LittleEndian.PutUint32(frameReply[17:], uint32(rotation))
			}
			if _, err := file.Write(frameReply[:]); err != nil {
				return err
			}
		case ipcInput, ipcUnlock, ipcSAS:
			var success bool
			switch op {
			case ipcInput:
				e, err := input.Unmarshal(data)
				if err != nil {
					return err
				}
				if in.Apply(e) == nil {
					held.Track(e)
					success = true
				}
			case ipcUnlock:
				for _, e := range held.Release() {
					_ = in.Apply(e)
				}
				success = enterPassword(data, valid, sas)
			case ipcSAS:
				if len(data) != 0 {
					return ErrUnavailable
				}
				success = sas()
			}
			clear(data)
			reply := byte(replyUnavailable)
			if success {
				reply = replyOK
			}
			if _, err := file.Write([]byte{reply}); err != nil {
				return err
			}
		default:
			return ErrUnavailable
		}
	}
	return nil
}

func enterPassword(secret []byte, valid, sas func() bool) bool {
	if len(secret) == 0 || !utf8.Valid(secret) || bytes.IndexByte(secret, 0) >= 0 {
		return false
	}
	var text [maxSecretBytes]uint16
	defer clear(text[:])
	n := 0
	for len(secret) > 0 {
		r, size := utf8.DecodeRune(secret)
		secret = secret[size:]
		if r <= 0xffff {
			text[n] = uint16(r)
			n++
		} else {
			first, second := utf16.EncodeRune(r)
			text[n], text[n+1] = uint16(first), uint16(second)
			n += 2
		}
	}
	if !valid() {
		return false
	}
	if sasRequired() && sas() {
		time.Sleep(500 * time.Millisecond)
	}
	if !valid() || C.kklock_tap(0x20) == 0 { // Space dismisses the lock screen curtain.
		return false
	}
	time.Sleep(450 * time.Millisecond)
	return valid() && C.kklock_password((*C.uint16_t)(unsafe.Pointer(&text[0])), C.uint32_t(n)) != 0
}

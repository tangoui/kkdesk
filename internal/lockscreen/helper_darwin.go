//go:build darwin && cgo

package lockscreen

/*
#include <stdlib.h>
#include "system_darwin.h"
*/
import "C"

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"io"
	"net"
	"os"
	"sync"
	"syscall"
	"time"
	"unicode/utf16"
	"unicode/utf8"
	"unsafe"

	"golang.org/x/sys/unix"

	"github.com/tangoui/kkdesk/internal/capture"
	"github.com/tangoui/kkdesk/internal/inject"
	"github.com/tangoui/kkdesk/internal/input"
)

type macCaptureBuffer struct {
	mu            sync.Mutex
	pixels        []byte
	width, height uint32
	change        uint64
}

func (b *macCaptureBuffer) update(f capture.Frame) {
	if f.Width <= 0 || f.Height <= 0 {
		return
	}
	_, n, ok := macFrameBounds(uint32(f.Width), uint32(f.Height), 1)
	if !ok {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if cap(b.pixels) < n {
		b.pixels = make([]byte, n)
	}
	pixels := b.pixels[:n]
	if C.kk_mac_read_nv12(C.uintptr_t(f.Pixels), (*C.uint8_t)(unsafe.Pointer(&pixels[0])),
		C.size_t(n), C.uint32_t(f.Width), C.uint32_t(f.Height)) == 0 {
		return
	}
	b.pixels, b.width, b.height = pixels, uint32(f.Width), uint32(f.Height)
	b.change++
}

func (b *macCaptureBuffer) present(mapping []byte, seen, seq uint64, reply []byte) (uint64, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.change == seen {
		return seen, false
	}
	off, n, ok := macFrameBounds(b.width, b.height, seq)
	if !ok || len(b.pixels) != n {
		return seen, false
	}
	copy(mapping[off:off+n], b.pixels)
	reply[0] = replyOK
	binary.LittleEndian.PutUint64(reply[1:], seq)
	binary.LittleEndian.PutUint32(reply[9:], b.width)
	binary.LittleEndian.PutUint32(reply[13:], b.height)
	return b.change, true
}

func macSharedFrames() ([]byte, *os.File, error) {
	var nonce [12]byte
	rand.Read(nonce[:])
	name := C.CString("/kkd-" + hex.EncodeToString(nonce[:]))
	defer C.free(unsafe.Pointer(name))
	var w, r C.int
	if code := C.kk_mac_shm(name, 2*macFrameSlotBytes, &w, &r); code != 0 {
		return nil, nil, syscall.Errno(code)
	}
	defer unix.Close(int(w))
	ro := os.NewFile(uintptr(r), "lock screen shared frames")
	data, err := unix.Mmap(int(w), 0, 2*macFrameSlotBytes, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
	if err != nil {
		_ = ro.Close()
		return nil, nil, err
	}
	return data, ro, nil
}

func serveMacSession(c *net.UnixConn, active func() bool) error {
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	var options [4]byte
	if _, err := io.ReadFull(c, options[:]); err != nil {
		return err
	}
	if !active() || !capture.HasPermission() || !inject.HasPermission() {
		return ErrUnavailable
	}
	valid := func() bool { return active() && macSocketAlive(c) }
	maxWidth := min(int(binary.LittleEndian.Uint16(options[:])), 5120)
	fps := max(1, min(int(binary.LittleEndian.Uint16(options[2:])), 60))
	in, err := inject.New()
	if err != nil {
		return err
	}
	mapping, descriptor, err := macSharedFrames()
	if err != nil {
		return err
	}
	defer unix.Munmap(mapping)
	defer descriptor.Close()
	var buffer macCaptureBuffer
	var held input.Held
	source, err := capture.Start(capture.Options{FPS: fps, MaxWidth: maxWidth}, func(f capture.Frame) {
		if valid() {
			buffer.update(f)
		}
	})
	if err != nil {
		return err
	}
	defer source.Close()
	defer func() {
		if valid() {
			for _, e := range held.Release() {
				_ = in.Apply(e)
			}
		}
	}()
	if !valid() {
		return ErrUnavailable
	}
	if err := macSendFD(c, int(descriptor.Fd())); err != nil {
		return err
	}
	_ = descriptor.Close()
	var seq, seen uint64
	var scratch [maxSecretBytes]byte
	defer clear(scratch[:])
	for valid() {
		_ = c.SetDeadline(time.Now().Add(3 * time.Second))
		op, data, err := receiveCommand(c, scratch[:])
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
			var reply [17]byte
			reply[0] = replyIdle
			if next, ok := buffer.present(mapping, seen, seq+1, reply[:]); ok {
				seen, seq = next, seq+1
			}
			if !valid() {
				return ErrUnavailable
			}
			if _, err := c.Write(reply[:]); err != nil {
				return err
			}
		case ipcInput, ipcUnlock:
			reply := byte(replyUnavailable)
			if op == ipcInput {
				event, err := input.Unmarshal(data)
				if err != nil {
					return err
				}
				if in.Apply(event) == nil {
					held.Track(event)
					reply = replyOK
				}
			} else {
				for _, event := range held.Release() {
					_ = in.Apply(event)
				}
				if macEnterPassword(in, data, valid) {
					reply = replyOK
				}
			}
			clear(data)
			if _, err := c.Write([]byte{reply}); err != nil {
				return err
			}
		default:
			return ErrUnavailable
		}
	}
	return nil
}

func macSocketAlive(c *net.UnixConn) bool {
	raw, err := c.SyscallConn()
	if err != nil {
		return false
	}
	alive := false
	err = raw.Control(func(fd uintptr) {
		var peek [1]byte
		n, _, err := unix.Recvfrom(int(fd), peek[:], unix.MSG_PEEK|unix.MSG_DONTWAIT)
		alive = n > 0 || err == unix.EAGAIN
	})
	return err == nil && alive
}

func macEnterPassword(in *inject.Injector, secret []byte, valid func() bool) bool {
	if len(secret) == 0 || !utf8.Valid(secret) || bytes.IndexByte(secret, 0) >= 0 || !valid() {
		return false
	}
	tap := func(key uint16, mods uint8) bool {
		down := input.Event{Kind: input.Key, HID: key, Mods: mods, Down: true}
		up := down
		up.Down = false
		return in.Apply(down) == nil && in.Apply(up) == nil
	}
	if !tap(0x2c, 0) { // Space can wake the password field.
		return false
	}
	time.Sleep(350 * time.Millisecond)
	if !valid() || !tap(0x04, input.ModMeta) { // Command+A selects the current field contents.
		return false
	}
	var text [maxSecretBytes]uint16
	defer clear(text[:])
	units := text[:0]
	for len(secret) > 0 {
		r, size := utf8.DecodeRune(secret)
		units = utf16.AppendRune(units, r)
		secret = secret[size:]
	}
	for len(units) > 0 {
		n := min(16, len(units))
		if n < len(units) && units[n-1] >= 0xd800 && units[n-1] <= 0xdbff {
			n--
		}
		if !valid() || C.kk_mac_inject_password((*C.uint16_t)(unsafe.Pointer(&units[0])), C.size_t(n)) == 0 {
			return false
		}
		units = units[n:]
	}
	return valid() && tap(0x28, 0)
}

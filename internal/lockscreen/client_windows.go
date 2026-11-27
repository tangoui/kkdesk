//go:build windows && amd64 && cgo

package lockscreen

/*
#include "desktop_windows.h"
*/
import "C"

import (
	"context"
	"encoding/binary"
	"io"
	"log"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/tangoui/kkdesk/internal/capture"
	"github.com/tangoui/kkdesk/internal/input"
	"github.com/tangoui/kkdesk/internal/winmedia"
)

func PlatformStart() StartFunc { return startWindows }

type windowsRemote struct {
	file  *os.File
	view  uintptr
	done  chan struct{}
	mu    sync.Mutex // requests and their replies share one stream
	close sync.Once
	fps   atomic.Int32
	w, h  atomic.Int32
	count atomic.Uint64
}

func startWindows(ctx context.Context, opts capture.Options, out func(capture.Frame)) (Session, error) {
	want, err := protectedExe()
	if err != nil {
		return nil, err
	}
	exe, err := os.Executable()
	if err != nil || !strings.EqualFold(exe, want) {
		return nil, ErrUnavailable
	}
	session := windows.WTSGetActiveConsoleSessionId()
	if !windowsSessionLocked(session) {
		return nil, ErrUnavailable
	}
	file, err := openPipe(session)
	if err != nil {
		return nil, err
	}
	ready := false
	defer func() {
		if !ready {
			_ = file.Close()
		}
	}()
	if err := file.SetDeadline(time.Now().Add(time.Second)); err != nil {
		return nil, err
	}
	var shared uint64
	if err := binary.Read(file, binary.LittleEndian, &shared); err != nil || shared == 0 {
		return nil, ErrUnavailable
	}
	h := windows.Handle(shared)
	defer windows.CloseHandle(h)
	view, err := windows.MapViewOfFile(h, windows.FILE_MAP_READ, 0, 0, 2*frameSlotBytes)
	if err != nil {
		return nil, err
	}
	s := &windowsRemote{file: file, view: view, done: make(chan struct{})}
	s.SetFPS(opts.FPS)
	go func() {
		select {
		case <-ctx.Done():
			s.disconnect()
		case <-s.done:
		}
	}()
	go s.frames(ctx, opts.MaxWidth, out)
	ready = true
	return s, nil
}

func (s *windowsRemote) Done() <-chan struct{} { return s.done }

func (s *windowsRemote) disconnect() { s.close.Do(func() { _ = s.file.Close() }) }

func (s *windowsRemote) Close() {
	s.disconnect()
	<-s.done
}

func (s *windowsRemote) SetFPS(fps int)   { s.fps.Store(int32(max(1, min(fps, 60)))) }
func (s *windowsRemote) Size() (int, int) { return int(s.w.Load()), int(s.h.Load()) }
func (s *windowsRemote) Stats() capture.Stats {
	return capture.Stats{Complete: s.count.Load()}
}

func (s *windowsRemote) command(op byte, payload, reply []byte, timeout time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.file.SetDeadline(time.Now().Add(timeout)); err != nil {
		return err
	}
	if err := sendCommand(s.file, op, payload); err != nil {
		s.disconnect()
		return err
	}
	if _, err := io.ReadFull(s.file, reply); err != nil {
		s.disconnect()
		return err
	}
	return nil
}

func (s *windowsRemote) Apply(e input.Event) error {
	op := ipcInput
	var payload []byte
	if e.Kind == input.Key && e.HID == 0x4c && e.Mods&(input.ModCtrl|input.ModAlt) == input.ModCtrl|input.ModAlt {
		if !e.Down {
			return nil
		}
		op = ipcSAS
	} else {
		payload = e.Marshal(nil)
	}
	var reply [1]byte
	if err := s.command(op, payload, reply[:], time.Second); err != nil {
		return err
	}
	if reply[0] != replyOK {
		return ErrUnavailable
	}
	return nil
}

func (s *windowsRemote) Unlock(ctx context.Context, secret []byte) error {
	deadline, ok := ctx.Deadline()
	timeout := 3 * time.Second
	if ok {
		timeout = min(timeout, time.Until(deadline))
	}
	if ctx.Err() != nil || timeout <= 0 {
		return ctx.Err()
	}
	if len(secret) == 0 || len(secret) > maxSecretBytes {
		return ErrUnavailable
	}
	var reply [1]byte
	if err := s.command(ipcUnlock, secret, reply[:], timeout); err != nil || reply[0] != replyOK {
		return ErrUnavailable
	}
	return nil
}

type lockedFrame struct {
	width, height, rotation uint32
	sequence                uint64
}

func (s *windowsRemote) frames(ctx context.Context, maxWidth int, out func(capture.Frame)) {
	defer close(s.done)
	defer s.disconnect()
	defer windows.UnmapViewOfFile(s.view)
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	winmedia.ComInit()
	var conv frameConverter
	defer conv.close()
	var lastSequence uint64
	var lastFrame time.Time
	var reply [21]byte
	for ctx.Err() == nil {
		began := time.Now()
		if err := s.command(ipcFrame, nil, reply[:], 2*time.Second); err != nil {
			return
		}
		switch reply[0] {
		case replyOK:
			f := lockedFrame{
				sequence: binary.LittleEndian.Uint64(reply[1:]),
				width:    binary.LittleEndian.Uint32(reply[9:]),
				height:   binary.LittleEndian.Uint32(reply[13:]),
				rotation: binary.LittleEndian.Uint32(reply[17:]),
			}
			off, _, ok := frameBounds(f.width, f.height, f.sequence)
			if !ok || f.sequence != lastSequence+1 || f.rotation > 4 {
				return
			}
			if err := conv.update(f, maxWidth, s.view+uintptr(off)); err != nil {
				log.Printf("kkdesk lock screen: frame conversion: %v", err)
				return
			}
			lastSequence = f.sequence
		case replyIdle:
		default:
			return
		}
		if conv.ready() && ctx.Err() == nil && (reply[0] == replyOK || time.Since(lastFrame) >= time.Second) {
			lastFrame = time.Now()
			s.w.Store(int32(conv.width))
			s.h.Store(int32(conv.height))
			s.count.Add(1)
			out(capture.Frame{Pixels: conv.texture, Width: conv.width, Height: conv.height, CaptureAt: lastFrame, ArrivedAt: lastFrame})
		}
		wait := time.Second/time.Duration(s.fps.Load()) - time.Since(began)
		if wait > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
			}
		}
	}
}

type frameConverter struct {
	last          lockedFrame
	width, height int
	bgra          unsafe.Pointer
	texture       uintptr
	conv          *winmedia.Converter
	generation    uint64
}

func (c *frameConverter) ready() bool { return c.texture != 0 }

func (c *frameConverter) close() {
	if c.conv != nil {
		c.conv.Close()
	}
	winmedia.Release(uintptr(c.bgra))
	winmedia.Release(c.texture)
	*c = frameConverter{}
}

func (c *frameConverter) update(f lockedFrame, maxWidth int, pixels uintptr) error {
	d, _, gen, err := winmedia.Device()
	if err != nil {
		return err
	}
	if c.last.width != f.width || c.last.height != f.height || c.last.rotation != f.rotation || c.generation != gen {
		c.close()
		w, h := int(f.width), int(f.height)
		if f.rotation == 2 || f.rotation == 4 {
			w, h = h, w
		}
		if maxWidth > 0 && w > maxWidth {
			h, w = h*maxWidth/w, maxWidth
		}
		c.width, c.height = max(2, w&^1), max(2, h&^1)
		c.last, c.generation = f, gen
		c.conv, err = winmedia.NewConverter(d, int(f.width), int(f.height), 87, c.width, c.height, int(f.rotation))
		if err != nil {
			return err
		}
		c.texture, err = winmedia.NewNV12(d, c.width, c.height)
		if err != nil {
			return err
		}
	}
	hr := int32(C.kklock_upload(winmedia.Ptr(d), &c.bgra, winmedia.Ptr(pixels), C.uint32_t(f.width), C.uint32_t(f.height)))
	if hr < 0 {
		return winmedia.Check("lock screen BGRA upload", hr)
	}
	return c.conv.Convert(uintptr(c.bgra), c.texture)
}

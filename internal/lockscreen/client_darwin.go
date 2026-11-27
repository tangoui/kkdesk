//go:build darwin && cgo

package lockscreen

/*
#include "system_darwin.h"
*/
import "C"

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"

	"github.com/tangoui/kkdesk/internal/capture"
	"github.com/tangoui/kkdesk/internal/input"
)

type macRemote struct {
	conn  *net.UnixConn
	view  []byte
	done  chan struct{}
	once  sync.Once
	mu    sync.Mutex
	fps   atomic.Int32
	w, h  atomic.Int32
	count atomic.Uint64
}

func startMac(ctx context.Context, opts capture.Options, out func(capture.Frame)) (Session, error) {
	if macOwnCode().team == "" {
		return nil, ErrNeedsSignature
	}
	if !macLocked(uint32(os.Geteuid())) {
		return nil, ErrUnavailable
	}
	c, err := macDial(ctx, macHost)
	if err != nil {
		return nil, err
	}
	stop := context.AfterFunc(ctx, func() { _ = c.Close() })
	defer stop()
	var options [4]byte
	binary.LittleEndian.PutUint16(options[:], uint16(max(0, min(opts.MaxWidth, 5120))))
	binary.LittleEndian.PutUint16(options[2:], uint16(max(1, min(opts.FPS, 60))))
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := c.Write(options[:]); err != nil {
		_ = c.Close()
		return nil, ErrUnavailable
	}
	fd, err := macReceiveFD(c)
	if err != nil {
		_ = c.Close()
		return nil, ErrUnavailable
	}
	defer unix.Close(fd)
	mode, err := unix.FcntlInt(uintptr(fd), unix.F_GETFL, 0)
	var st unix.Stat_t
	if err != nil || mode&unix.O_ACCMODE != unix.O_RDONLY || (unix.Fstat(fd, &st) != nil) ||
		st.Size != 2*macFrameSlotBytes {
		_ = c.Close()
		return nil, ErrUnavailable
	}
	view, err := unix.Mmap(fd, 0, 2*macFrameSlotBytes, unix.PROT_READ, unix.MAP_SHARED)
	if err != nil {
		_ = c.Close()
		return nil, err
	}
	s := &macRemote{conn: c, view: view, done: make(chan struct{})}
	s.SetFPS(opts.FPS)
	go func() {
		select {
		case <-ctx.Done():
			s.disconnect()
		case <-s.done:
		}
	}()
	go s.frames(ctx, out)
	return s, nil
}

func (s *macRemote) Done() <-chan struct{} { return s.done }

func (s *macRemote) disconnect() { s.once.Do(func() { _ = s.conn.Close() }) }

func (s *macRemote) Close() {
	s.disconnect()
	<-s.done
}

func (s *macRemote) SetFPS(fps int)       { s.fps.Store(int32(max(1, min(fps, 60)))) }
func (s *macRemote) Size() (int, int)     { return int(s.w.Load()), int(s.h.Load()) }
func (s *macRemote) Stats() capture.Stats { return capture.Stats{Complete: s.count.Load()} }

func (s *macRemote) command(op byte, payload, reply []byte, timeout time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		return err
	}
	if err := sendCommand(s.conn, op, payload); err != nil {
		s.disconnect()
		return err
	}
	if _, err := io.ReadFull(s.conn, reply); err != nil {
		s.disconnect()
		return err
	}
	return nil
}

func (s *macRemote) Apply(e input.Event) error {
	var reply [1]byte
	if err := s.command(ipcInput, e.Marshal(nil), reply[:], time.Second); err != nil || reply[0] != replyOK {
		return ErrUnavailable
	}
	return nil
}

func (s *macRemote) Unlock(ctx context.Context, secret []byte) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	timeout := 2 * time.Second
	if deadline, ok := ctx.Deadline(); ok {
		timeout = min(timeout, time.Until(deadline))
	}
	var reply [1]byte
	if err := s.command(ipcUnlock, secret, reply[:], timeout); err != nil || reply[0] != replyOK {
		return ErrUnavailable
	}
	return nil
}

func (s *macRemote) frames(ctx context.Context, out func(capture.Frame)) {
	defer close(s.done)
	defer s.disconnect()
	defer unix.Munmap(s.view)
	var last uint64
	var lastFrame time.Time
	var pixel C.uintptr_t
	defer func() {
		if pixel != 0 {
			C.kk_mac_release_nv12(pixel)
		}
	}()
	var reply [17]byte
	for ctx.Err() == nil {
		began := time.Now()
		if err := s.command(ipcFrame, nil, reply[:], time.Second); err != nil {
			return
		}
		switch reply[0] {
		case replyOK:
			seq := binary.LittleEndian.Uint64(reply[1:])
			w := binary.LittleEndian.Uint32(reply[9:])
			h := binary.LittleEndian.Uint32(reply[13:])
			off, n, ok := macFrameBounds(w, h, seq)
			if !ok || seq != last+1 {
				return
			}
			next := C.kk_mac_make_nv12((*C.uint8_t)(unsafe.Pointer(&s.view[off])), C.size_t(n), C.uint32_t(w), C.uint32_t(h))
			if next == 0 {
				return
			}
			if pixel != 0 {
				C.kk_mac_release_nv12(pixel)
			}
			pixel, last = next, seq
			s.w.Store(int32(w))
			s.h.Store(int32(h))
		case replyIdle:
		default:
			return
		}
		if ctx.Err() == nil && pixel != 0 && (reply[0] == replyOK || time.Since(lastFrame) >= time.Second) {
			lastFrame = time.Now()
			w, h := s.Size()
			s.count.Add(1)
			out(capture.Frame{Pixels: uintptr(pixel), Width: w, Height: h, CaptureAt: lastFrame, ArrivedAt: lastFrame})
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

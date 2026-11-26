//go:build windows && cgo

package codec

/*
#cgo CXXFLAGS: -std=c++17 -O2 -Wall -Wno-unknown-pragmas
#cgo windows LDFLAGS: -ld3d11 -ldxgi -lmfplat -lmf -lmfuuid -lole32 -luuid -lstrmiids -static-libgcc -static-libstdc++ -static
#include <stdlib.h>
#include "encode_windows.h"
*/
import "C"

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/tangoui/kkdesk/internal/winmedia"
)

// Result codes from encode_windows.h.
const (
	encSRetry  = 2
	encEDevice = int32(-0x5F21FFFF) // 0xA0DE0001: texture from another D3D11 device
	encESize   = int32(-0x5F21FFFE) // 0xA0DE0002: texture size/format mismatch

	evNeedInput     = 601
	evHaveOutput    = 602
	evDrainComplete = 603
)

const (
	// creditWait is how long Encode waits for an async MFT to ask for
	// input before dropping the frame (the capture thread must not stall).
	creditWait = 20 * time.Millisecond
	// hwGiveUpDrops: a hardware MFT that never asked for input or produced
	// output for this many frames is replaced by the software encoder.
	hwGiveUpDrops = 60
)

//export mmencLog
func mmencLog(msg *C.char) { winmedia.Logf("%s", C.GoString(msg)) }

// mfInst is one created MFT with its event loop.
type mfInst struct {
	enc       *C.mmEncW
	hw        bool
	async     bool
	credits   chan struct{}
	done      chan struct{} // closed when the event loop exits
	drained   chan struct{} // closed on METransformDrainComplete
	drainOnce sync.Once
	stop      atomic.Bool
}

// mfEncoder is the Media Foundation H.264 encoder. Frame.Pixels is an NV12
// ID3D11Texture2D* on winmedia.Device(); it is copied (GPU) into an
// encoder-owned texture before Encode returns.
type mfEncoder struct {
	out  EncodeOutput
	w, h int

	mu         sync.Mutex
	inst       *mfInst
	info       string
	allowHW    bool   // false after a fallback or with KKDESK_ENCODER=software
	swReason   string // why the software encoder is used
	fps, bps   int
	pendingKey bool
	drops      int // consecutive drops
	submitted  uint64
	dropped    uint64
	errLogs    int

	clockMu sync.Mutex
	clock   sampleClock

	outMu      sync.Mutex
	ps         paramSets
	outputs    atomic.Uint64
	outLogged  bool
	badOutLogs int

	errMu   sync.Mutex
	lastErr error
}

// NewEncoder creates a Media Foundation H.264 encoder for w×h NV12
// textures: the first usable hardware MFT on the capture adapter, else the
// software CMSH264EncoderMFT (also when KKDESK_ENCODER=software). Output is
// delivered to out on the encoder's event goroutine (hardware) or inside
// Encode (software). The returned value also implements Info() string.
func NewEncoder(w, h, fps, bps int, out EncodeOutput) (Encoder, error) {
	if w <= 0 || h <= 0 || w%2 != 0 || h%2 != 0 || fps <= 0 || bps <= 0 || out == nil {
		return nil, fmt.Errorf("codec: invalid encoder parameters %dx%d fps=%d bps=%d", w, h, fps, bps)
	}
	e := &mfEncoder{out: out, w: w, h: h, fps: fps, bps: bps}
	e.allowHW = true
	if v := os.Getenv("KKDESK_ENCODER"); v == "software" {
		e.allowHW, e.swReason = false, "KKDESK_ENCODER=software"
	}
	if err := e.open(); err != nil {
		return nil, err
	}
	return e, nil
}

// Info describes the encoder in use.
func (e *mfEncoder) Info() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.info
}

// open creates the MFT (hardware first unless e.allowHW is false) on the
// current shared device. Called with mu held (or before e is shared).
func (e *mfEncoder) open() error {
	allowHW, swReason := e.allowHW, e.swReason
	dev, mgr, _, err := winmedia.Device()
	if err != nil {
		return fmt.Errorf("codec: %w", err)
	}
	cfg := C.mmencConfig{width: C.int32_t(e.w), height: C.int32_t(e.h), fps: C.int32_t(e.fps), bps: C.int32_t(e.bps)}
	var enc *C.mmEncW
	in := &mfInst{}
	if allowHW {
		var name [256]C.char
		hr := int32(C.mmenc_create_hw(winmedia.Ptr(dev), winmedia.Ptr(mgr), C.uint64_t(winmedia.AdapterLUID()), &cfg,
			&enc, &name[0], C.int32_t(len(name))))
		if hr >= 0 && enc != nil {
			in.hw = true
			e.info = fmt.Sprintf("MF H.264 hardware: %s, %dx%d", C.GoString(&name[0]), e.w, e.h)
		} else {
			swReason = "no usable hardware encoder (" + winmedia.HR(hr) + ")"
			enc = nil
		}
	}
	if enc == nil {
		hr := int32(C.mmenc_create_sw(winmedia.Ptr(dev), &cfg, &enc))
		if hr < 0 || enc == nil {
			return fmt.Errorf("codec: no H.264 encoder: %s; software CMSH264EncoderMFT: %s", swReason, winmedia.HR(hr))
		}
		e.info = fmt.Sprintf("MF H.264 software (CMSH264EncoderMFT): %s, %dx%d", swReason, e.w, e.h)
	}
	in.enc = enc
	in.async = C.mmenc_is_async(enc) != 0
	e.learnSeqHeader(in)
	winmedia.Logf("encoder: using %s (async %v)", e.info, in.async)
	if in.async {
		in.credits = make(chan struct{}, 32)
		in.done = make(chan struct{})
		in.drained = make(chan struct{})
		go e.loop(in)
	}
	e.inst = in
	e.drops = 0
	e.pendingKey = true // a new encoder starts with an IDR anyway; keep SPS/PPS logic simple
	return nil
}

func (e *mfEncoder) learnSeqHeader(in *mfInst) {
	var p *C.uint8_t
	var n C.int32_t
	C.mmenc_seq_header(in.enc, &p, &n)
	if p == nil || n <= 0 {
		return
	}
	hdr := C.GoBytes(unsafe.Pointer(p), C.int(n))
	e.outMu.Lock()
	ok := e.ps.learn(hdr)
	e.outMu.Unlock()
	winmedia.Logf("encoder: MF_MT_MPEG_SEQUENCE_HEADER %d bytes, SPS/PPS found: %v", len(hdr), ok)
}

// loop handles the async MFT's events on a dedicated OS thread.
func (e *mfEncoder) loop(in *mfInst) {
	defer close(in.done)
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	C.mmenc_thread_init()
	block := C.mmenc_can_block(in.enc) != 0
	unknown := 0
	for {
		var typ C.uint32_t
		var st C.int32_t
		w := C.int32_t(0)
		if block {
			w = 1
		}
		hr := int32(C.mmenc_get_event(in.enc, w, &typ, &st))
		if hr == winmedia.MFENoEventsAvailable {
			if in.stop.Load() {
				return
			}
			time.Sleep(time.Millisecond)
			continue
		}
		if hr < 0 {
			if hr != winmedia.MFEShutdown || !in.stop.Load() {
				winmedia.Logf("encoder: GetEvent: %s; event loop stops", winmedia.HR(hr))
				e.setErr(winmedia.Check("encoder GetEvent", hr))
			}
			return
		}
		if int32(st) < 0 {
			winmedia.Logf("encoder: event %d with failure status %s", uint32(typ), winmedia.HR(int32(st)))
			e.setErr(winmedia.Check(fmt.Sprintf("encoder event %d", uint32(typ)), int32(st)))
		}
		switch typ {
		case evNeedInput:
			select {
			case in.credits <- struct{}{}:
			default:
			}
		case evHaveOutput:
			for i := 0; i < 3; i++ {
				if hr := e.pull(in); hr != encSRetry {
					if hr < 0 {
						e.logErr("encoder: ProcessOutput: %s", winmedia.HR(hr))
					}
					break
				}
			}
		case evDrainComplete:
			in.drainOnce.Do(func() { close(in.drained) })
		default:
			if unknown++; unknown <= 5 {
				winmedia.Logf("encoder: ignoring MFT event %d", uint32(typ))
			}
		}
	}
}

// pull fetches one output sample and delivers it. It returns the
// mmenc_output result (S_OK, encSRetry or a failure such as
// MF_E_TRANSFORM_NEED_MORE_INPUT).
func (e *mfEncoder) pull(in *mfInst) int32 {
	var o C.mmencOut
	hr := int32(C.mmenc_output(in.enc, &o))
	if hr == encSRetry {
		e.learnSeqHeader(in)
		return hr
	}
	if hr < 0 || o.len <= 0 || o.data == nil {
		return hr
	}
	raw := unsafe.Slice((*byte)(unsafe.Pointer(o.data)), int(o.len))
	au, err := normalizeAnnexB(raw)
	e.outMu.Lock()
	if err != nil {
		if e.badOutLogs++; e.badOutLogs <= 5 {
			n := len(raw)
			if n > 16 {
				n = 16
			}
			winmedia.Logf("encoder: dropping unrecognised output (%d bytes, starts %x): %v", len(raw), raw[:n], err)
		}
		e.outMu.Unlock()
		return hr
	}
	au, key := e.ps.fix(au)
	if !e.outLogged {
		e.outLogged = true
		n := len(raw)
		if n > 8 {
			n = 8
		}
		winmedia.Logf("encoder: first output %d bytes (raw starts %x), keyframe %v, clean point %d", len(au), raw[:n],
			key, int(o.cleanPoint))
	}
	e.outMu.Unlock()
	captureAt := time.Now()
	if o.hasTime != 0 {
		e.clockMu.Lock()
		captureAt = e.clock.at(int64(o.sampleTime), captureAt)
		e.clockMu.Unlock()
	}
	e.outputs.Add(1)
	e.out(au, captureAt, key)
	return hr
}

func (e *mfEncoder) setErr(err error) {
	e.errMu.Lock()
	e.lastErr = err
	e.errMu.Unlock()
}

func (e *mfEncoder) logErr(format string, args ...any) {
	e.outMu.Lock()
	e.badOutLogs++
	n := e.badOutLogs
	e.outMu.Unlock()
	if n <= 10 || n%500 == 0 {
		winmedia.Logf(format, args...)
	}
}

func (e *mfEncoder) Encode(f Frame, forceKey bool) error {
	if f.Pixels == 0 {
		return errors.New("codec: nil texture")
	}
	e.errMu.Lock()
	err := e.lastErr
	e.lastErr = nil
	e.errMu.Unlock()
	if err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	in := e.inst
	if in == nil {
		return errors.New("codec: encoder closed")
	}
	force := forceKey || e.pendingKey
	if in.async {
		select {
		case <-in.credits:
		default:
			t := time.NewTimer(creditWait)
			select {
			case <-in.credits:
				t.Stop()
			case <-t.C:
				return e.drop(in, force, "the encoder did not ask for input in time")
			}
		}
	}
	e.clockMu.Lock()
	ts := e.clock.stamp(f.CaptureAt)
	e.clockMu.Unlock()
	dur := int64(10_000_000 / e.fps)
	fk := C.int32_t(0)
	if force {
		fk = 1
	}
	// Pixels is a texture pointer from C, not Go memory.
	hr := int32(C.mmenc_input(in.enc, winmedia.Ptr(f.Pixels), C.int64_t(ts), C.int64_t(dur), fk))
	switch {
	case hr == encEDevice:
		winmedia.Logf("encoder: input texture is on another D3D11 device (device re-created); re-creating the encoder")
		e.closeInst(in, false)
		e.inst = nil
		return e.open()
	case hr == encESize:
		return fmt.Errorf("codec: input texture does not match the %dx%d NV12 encoder", e.w, e.h)
	case hr == winmedia.MFENotAccepting:
		return e.drop(in, force, "MF_E_NOTACCEPTING")
	case hr < 0:
		if in.hw && e.outputs.Load() == 0 {
			return e.fallback(in, "hardware ProcessInput failed before any output: "+winmedia.HR(hr))
		}
		if e.errLogs++; e.errLogs <= 10 || e.errLogs%500 == 0 {
			winmedia.Logf("encoder: ProcessInput: %s", winmedia.HR(hr))
		}
		return winmedia.Check("codec: ProcessInput", hr)
	}
	e.pendingKey = false
	e.drops = 0
	e.submitted++
	if e.submitted == 1 {
		winmedia.Logf("encoder: first frame submitted (keyframe requested %v)", force)
	}
	if !in.async {
		for i := 0; i < 16; i++ {
			hr := e.pull(in)
			if hr == encSRetry {
				continue
			}
			if hr < 0 {
				if hr != winmedia.MFETransformNeedMore {
					e.logErr("encoder: ProcessOutput: %s", winmedia.HR(hr))
				}
				break
			}
		}
	}
	return nil
}

// drop skips a frame the encoder cannot take now, carrying a keyframe
// request over to the next frame. A hardware encoder that never works is
// replaced by the software one.
func (e *mfEncoder) drop(in *mfInst, force bool, why string) error {
	e.dropped++
	e.drops++
	e.pendingKey = force
	if e.dropped <= 5 || e.dropped%300 == 0 {
		winmedia.Logf("encoder: frame dropped (%s); %d dropped so far", why, e.dropped)
	}
	if in.hw && e.outputs.Load() == 0 && e.drops >= hwGiveUpDrops {
		return e.fallback(in, fmt.Sprintf("hardware encoder produced no output after %d frames", e.drops))
	}
	return nil
}

// fallback replaces the hardware MFT by the software encoder.
func (e *mfEncoder) fallback(in *mfInst, reason string) error {
	winmedia.Logf("encoder: %s; switching to the software encoder", reason)
	e.closeInst(in, false)
	e.inst = nil
	e.allowHW, e.swReason = false, reason
	if err := e.open(); err != nil {
		winmedia.Logf("encoder: software fallback failed: %v", err)
		return err
	}
	return nil
}

// closeInst stops and releases an MFT; with drain it first flushes
// pending output. Called with mu held.
func (e *mfEncoder) closeInst(in *mfInst, drain bool) {
	if in.async {
		if drain {
			if hr := int32(C.mmenc_drain(in.enc)); hr < 0 {
				winmedia.Logf("encoder: drain: %s", winmedia.HR(hr))
			} else {
				select {
				case <-in.drained:
				case <-time.After(500 * time.Millisecond):
					winmedia.Logf("encoder: drain did not complete within 500 ms")
				}
			}
		}
		in.stop.Store(true)
		C.mmenc_shutdown(in.enc)
		select {
		case <-in.done:
		case <-time.After(2 * time.Second):
			// Releasing the MFT under a running GetEvent could crash;
			// leak it instead.
			winmedia.Logf("encoder: event loop did not stop within 2 s; leaking the MFT")
			return
		}
	} else if drain {
		C.mmenc_drain(in.enc)
		for i := 0; i < 16; i++ {
			if hr := e.pull(in); hr < 0 {
				break
			}
		}
	}
	C.mmenc_destroy(in.enc)
	in.enc = nil
}

func (e *mfEncoder) SetBitrate(bps int) {
	if bps <= 0 {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.bps = bps
	if e.inst == nil {
		return
	}
	if hr := int32(C.mmenc_set_bitrate(e.inst.enc, C.uint32_t(bps))); hr < 0 {
		winmedia.Logf("encoder: SetBitrate(%d): CODECAPI_AVEncCommonMeanBitRate %s", bps, winmedia.HR(hr))
	}
}

// SetFPS updates the sample durations and the keyframe interval (fps*2);
// the frame rate in the media type stays as created (changing it would
// need a new MFT).
func (e *mfEncoder) SetFPS(fps int) {
	if fps <= 0 {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if fps == e.fps {
		return
	}
	e.fps = fps
	if e.inst == nil {
		return
	}
	hr := int32(C.mmenc_set_gop(e.inst.enc, C.uint32_t(fps*2)))
	winmedia.Logf("encoder: SetFPS(%d): GOP %d frames (%s); media type frame rate unchanged", fps, fps*2, winmedia.HR(hr))
}

func (e *mfEncoder) Close() {
	e.mu.Lock()
	defer e.mu.Unlock()
	in := e.inst
	if in == nil {
		return
	}
	e.closeInst(in, true)
	e.inst = nil
	winmedia.Logf("encoder: closed (%s): %d submitted, %d output, %d dropped", e.info, e.submitted,
		e.outputs.Load(), e.dropped)
}

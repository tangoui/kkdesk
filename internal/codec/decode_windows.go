//go:build windows && cgo

package codec

/*
#cgo CXXFLAGS: -std=c++17 -O2 -Wall -Wno-unknown-pragmas
#cgo windows LDFLAGS: -ld3d11 -ldxgi -lmfplat -lmf -lmfuuid -lole32 -luuid -lstrmiids -static-libgcc -static-libstdc++ -static
#include <stdlib.h>
#include "decode_windows.h"
*/
import "C"

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"time"
	"unsafe"

	"github.com/tangoui/kkdesk/internal/winmedia"
)

// Results from decode_windows.h and mferror.h.
const (
	decSRetry          = 2
	mfENotAccepting    = int32(0xC00D36B5 - 1<<32) // MF_E_NOTACCEPTING
	mfENeedMoreInput   = int32(0xC00D6D72 - 1<<32) // MF_E_TRANSFORM_NEED_MORE_INPUT
	decMaxOutputPerAU  = 16                        // guards against a decoder that never reports NEED_MORE_INPUT
	decInfoBufferBytes = 256
	decMaxPending      = 64
)

//export mmdecLog
func mmdecLog(msg *C.char) { winmedia.Logf("%s", C.GoString(msg)) }

// mfDecoder is the Media Foundation H.264 decoder. Pictures are delivered
// as a decoder-owned w×h NV12 ID3D11Texture2D* on winmedia.Device(),
// valid only during the DecodeOutput call.
type mfDecoder struct {
	out DecodeOutput

	mu       sync.Mutex
	dec      *C.mmDecW
	gen      uint64 // winmedia device generation dec was created on
	software bool
	info     string
	sawSPS   bool
	closed   bool
	// captures maps the 100 ns sample time passed to the MFT back to the
	// exact captureAt (the MFT only carries 100 ns units).
	captures map[int64]time.Time
}

// NewDecoder creates the H.264 decoder: D3D11 hardware decoding through
// the shared device when the in-box decoder supports it, software decoding
// otherwise (also when KKDESK_DECODER=software). The returned value also
// implements Info() string.
func NewDecoder(out DecodeOutput) (Decoder, error) {
	if out == nil {
		return nil, errors.New("codec: nil decoder output")
	}
	d := &mfDecoder{out: out, software: os.Getenv("KKDESK_DECODER") == "software", captures: map[int64]time.Time{}}
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.open(); err != nil {
		return nil, err
	}
	return d, nil
}

// Info describes the decoder in use.
func (d *mfDecoder) Info() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.info
}

// open (re)creates the MFT on the current device. d.mu must be held.
func (d *mfDecoder) open() error {
	winmedia.ComInit()
	device, manager, gen, err := winmedia.Device()
	if err != nil {
		return err
	}
	if d.dec != nil {
		C.mmdec_destroy(d.dec)
		d.dec = nil
	}
	sw := C.int32_t(0)
	if d.software {
		sw = 1
	}
	info := (*C.char)(C.malloc(decInfoBufferBytes))
	defer C.free(unsafe.Pointer(info))
	*info = 0
	var dec *C.mmDecW
	if hr := int32(C.mmdec_create(winmedia.Ptr(device), winmedia.Ptr(manager), sw, &dec, info, decInfoBufferBytes)); hr < 0 {
		return winmedia.Check("create H.264 decoder", hr)
	}
	d.dec, d.gen, d.info = dec, gen, C.GoString(info)
	d.sawSPS = false
	return nil
}

func (d *mfDecoder) Decode(annexB []byte, captureAt time.Time) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return errors.New("codec: decoder closed")
	}
	hasSPS, hasSlice := false, false
	for _, n := range splitAnnexB(annexB) {
		if len(n) == 0 {
			continue
		}
		switch n[0] & 0x1f {
		case 7:
			hasSPS = true
		case 1, 5:
			hasSlice = true
		}
	}
	if _, _, gen, err := winmedia.Device(); err != nil {
		return err
	} else if gen != d.gen {
		winmedia.Logf("decoder: D3D11 device changed; recreating the decoder")
		if err := d.open(); err != nil {
			return err
		}
	}
	if hasSPS {
		d.sawSPS = true
	}
	if hasSlice && !d.sawSPS {
		return errors.New("codec: slice before SPS/PPS")
	}
	if len(annexB) == 0 {
		return nil
	}
	winmedia.ComInit()
	buf := C.CBytes(annexB)
	defer C.free(buf)
	ts := C.int64_t(captureAt.UnixNano() / 100)
	if len(d.captures) >= decMaxPending {
		clear(d.captures) // pictures that never came out (corrupt input)
	}
	d.captures[int64(ts)] = captureAt
	hr := int32(C.mmdec_input(d.dec, (*C.uint8_t)(buf), C.int32_t(len(annexB)), ts))
	if hr == mfENotAccepting {
		if err := d.drain(); err != nil {
			return err
		}
		hr = int32(C.mmdec_input(d.dec, (*C.uint8_t)(buf), C.int32_t(len(annexB)), ts))
	}
	if hr < 0 {
		return winmedia.Check("ProcessInput", hr)
	}
	return d.drain()
}

// drain delivers every picture the MFT has ready. d.mu must be held.
func (d *mfDecoder) drain() error {
	for i := 0; i < decMaxOutputPerAU; i++ {
		var o C.mmdecOut
		hr := int32(C.mmdec_output(d.dec, &o))
		switch {
		case hr == mfENeedMoreInput:
			return nil
		case hr == decSRetry:
			continue
		case hr < 0:
			return winmedia.Check("ProcessOutput", hr)
		}
		ts := int64(o.sampleTime)
		at, ok := d.captures[ts]
		if ok {
			delete(d.captures, ts)
		} else {
			at = time.Unix(0, ts*100)
		}
		d.out(uintptr(o.texture), at)
	}
	return fmt.Errorf("codec: decoder produced more than %d pictures for one access unit", decMaxOutputPerAU)
}

func (d *mfDecoder) Close() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return
	}
	d.closed = true
	if d.dec != nil {
		C.mmdec_destroy(d.dec)
		d.dec = nil
	}
}

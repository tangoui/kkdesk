//go:build darwin && cgo

package codec

/*
#include <stdlib.h>
#include "vt_darwin.h"
*/
import "C"

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"runtime/cgo"
	"sync"
	"time"
)

// vtDecoder is the VideoToolbox H.264 decoder.
type vtDecoder struct {
	mu     sync.Mutex
	dec    *C.mmDec
	handle cgo.Handle
	out    DecodeOutput
	sps    []byte
	pps    []byte
	avcc   []byte
	cbErr  error
	// SPS/PPS the current session was created from.
	curSPS []byte
	curPPS []byte
}

// NewDecoder creates a hardware H.264 decoder (VideoToolbox). The session is
// created from the first SPS/PPS in the stream and recreated when they change.
// Output is NV12 video-range, IOSurface-backed CVPixelBuffers delivered to out
// before Decode returns.
func NewDecoder(out DecodeOutput) (Decoder, error) {
	if out == nil {
		return nil, errors.New("codec: nil decoder output")
	}
	d := &vtDecoder{out: out}
	d.handle = cgo.NewHandle(d)
	d.dec = C.mmDecNew(C.uintptr_t(d.handle))
	return d, nil
}

func (d *vtDecoder) Decode(annexB []byte, captureAt time.Time) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.dec == nil {
		return errors.New("codec: decoder closed")
	}
	var sps, pps []byte
	d.avcc = d.avcc[:0]
	for _, n := range splitAnnexB(annexB) {
		if len(n) == 0 {
			continue
		}
		switch n[0] & 0x1f {
		case 7:
			sps = n
		case 8:
			pps = n
		case 9, 6: // AUD, SEI: not needed by the decoder
		default:
			d.avcc = binary.BigEndian.AppendUint32(d.avcc, uint32(len(n)))
			d.avcc = append(d.avcc, n...)
		}
	}
	if sps != nil {
		d.sps = append(d.sps[:0:0], sps...)
	}
	if pps != nil {
		d.pps = append(d.pps[:0:0], pps...)
	}
	if (sps != nil || pps != nil) && d.sps != nil && d.pps != nil {
		if err := d.setParams(); err != nil {
			return err
		}
	}
	if len(d.avcc) == 0 {
		return nil
	}
	if C.mmDecHasSession(d.dec) == 0 {
		return errors.New("codec: slice before SPS/PPS")
	}
	d.cbErr = nil
	buf := C.CBytes(d.avcc)
	defer C.free(buf)
	st := C.mmDecDecode(d.dec, (*C.uint8_t)(buf), C.size_t(len(d.avcc)), C.int64_t(captureAt.UnixNano()))
	if st != 0 {
		return fmt.Errorf("codec: VTDecompressionSessionDecodeFrame failed: OSStatus %d", int32(st))
	}
	return d.cbErr
}

// setParams recreates the session if SPS/PPS differ from the active ones.
func (d *vtDecoder) setParams() error {
	if C.mmDecHasSession(d.dec) != 0 && bytes.Equal(d.sps, d.curSPS) && bytes.Equal(d.pps, d.curPPS) {
		return nil
	}
	s := C.CBytes(d.sps)
	p := C.CBytes(d.pps)
	defer C.free(s)
	defer C.free(p)
	st := C.mmDecSetParams(d.dec, (*C.uint8_t)(s), C.size_t(len(d.sps)), (*C.uint8_t)(p), C.size_t(len(d.pps)))
	if st != 0 {
		d.curSPS, d.curPPS = nil, nil
		return fmt.Errorf("codec: creating decode session failed: OSStatus %d", int32(st))
	}
	d.curSPS = append([]byte(nil), d.sps...)
	d.curPPS = append([]byte(nil), d.pps...)
	return nil
}

func (d *vtDecoder) Close() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.dec == nil {
		return
	}
	C.mmDecDestroy(d.dec)
	d.dec = nil
	d.handle.Delete()
}

//export mmDecOutput
func mmDecOutput(h C.uintptr_t, pix C.uintptr_t, capNanos C.int64_t, status C.int32_t) {
	d := cgo.Handle(h).Value().(*vtDecoder)
	if status != 0 {
		// Called synchronously from Decode, which holds d.mu.
		d.cbErr = fmt.Errorf("codec: decode failed: OSStatus %d", int32(status))
		return
	}
	d.out(uintptr(pix), time.Unix(0, int64(capNanos)))
}

// Package codec provides H.264 video encoding and decoding behind
// platform-independent interfaces. The darwin implementation uses
// VideoToolbox hardware sessions; the windows encoder uses Media Foundation
// (hardware MFT, software fallback); other platforms currently return
// ErrUnsupported.
package codec

import (
	"errors"
	"time"
)

// ErrUnsupported is returned by NewEncoder/NewDecoder on platforms (or builds
// without cgo) that have no implementation yet.
var ErrUnsupported = errors.New("codec: not supported on this platform")

// Frame is one raw video frame to encode.
type Frame struct {
	// Pixels is the platform pixel buffer. On darwin it is a CVPixelBufferRef
	// (NV12 preferred); the caller keeps it retained for the duration of the
	// Encode call and may release it afterwards (the encoder retains it as
	// long as it needs it). On windows it is an NV12 ID3D11Texture2D* on
	// winmedia.Device(); the encoder copies it before Encode returns.
	Pixels uintptr
	// CaptureAt is when the frame was captured. It is passed through to the
	// encoder output unchanged.
	CaptureAt time.Time
}

// EncodeOutput receives one encoded access unit as Annex-B with 4-byte start
// codes. Keyframes start with SPS and PPS. nal is only valid during the call.
// It is called on an encoder-owned thread.
type EncodeOutput func(nal []byte, captureAt time.Time, keyframe bool)

// DecodeOutput receives one decoded picture. On darwin pix is a
// CVPixelBufferRef (NV12, IOSurface-backed) that is only valid during the
// call; the receiver must CVPixelBufferRetain it to keep it. It is called on a
// decoder-owned thread, before the corresponding Decode call returns.
type DecodeOutput func(pix uintptr, captureAt time.Time)

// Encoder encodes raw frames to H.264.
type Encoder interface {
	// Encode submits a frame. forceKey requests an IDR (e.g. on PLI).
	Encode(frame Frame, forceKey bool) error
	// SetBitrate changes the target average bitrate in bits per second.
	SetBitrate(bps int)
	// SetFPS changes the expected frame rate (and the keyframe interval,
	// which is fps*2 frames).
	SetFPS(fps int)
	// Close flushes pending frames (delivering their output) and releases the
	// encoder. Encode must not be called afterwards.
	Close()
}

// Decoder decodes H.264 Annex-B access units.
type Decoder interface {
	// Decode decodes one access unit. Parameter sets (SPS/PPS) in the stream
	// (re)create the decoding session; slices before the first SPS/PPS are
	// rejected with an error.
	Decode(annexB []byte, captureAt time.Time) error
	// Close releases the decoder.
	Close()
}

// splitAnnexB splits an Annex-B byte stream into NAL units (without start
// codes). Both 3- and 4-byte start codes are recognised.
func splitAnnexB(b []byte) [][]byte {
	var nals [][]byte
	start := -1
	i := 0
	for i+3 <= len(b) {
		if b[i] == 0 && b[i+1] == 0 && b[i+2] == 1 {
			if start >= 0 {
				end := i
				if end > start && b[end-1] == 0 {
					end--
				}
				nals = append(nals, b[start:end])
			}
			i += 3
			start = i
			continue
		}
		i++
	}
	if start >= 0 && start < len(b) {
		nals = append(nals, b[start:])
	}
	return nals
}

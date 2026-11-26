package session

import (
	"testing"

	"github.com/pion/rtp"
)

// A host that never sets the marker bit cannot grow the viewer's frame
// buffer without bound.
func TestAssemblerBoundsFrameSize(t *testing.T) {
	var a assembler
	payload := make([]byte, 1<<20) // one 1 MiB NAL unit (single NAL packet)
	payload[0] = 0x41              // non-IDR slice
	lost := false
	for i := range 40 {
		_, _, ok, l := a.push(&rtp.Packet{Header: rtp.Header{SequenceNumber: uint16(i), Timestamp: 9000}, Payload: payload})
		if ok {
			t.Fatal("a frame was completed without a marker")
		}
		lost = lost || l
		if len(a.buf) > maxFrameBytes {
			t.Fatalf("frame buffer grew to %d bytes, limit %d", len(a.buf), maxFrameBytes)
		}
	}
	if !lost {
		t.Error("an oversized frame was not reported as lost (no keyframe request)")
	}
	frame, _, ok, _ := a.push(&rtp.Packet{Header: rtp.Header{SequenceNumber: 40, Timestamp: 9000, Marker: true}, Payload: payload})
	if ok || frame != nil {
		t.Fatalf("oversized frame delivered (%d bytes)", len(frame))
	}
	// The next frame is assembled normally.
	small := []byte{0x65, 1, 2, 3}
	frame, _, ok, _ = a.push(&rtp.Packet{Header: rtp.Header{SequenceNumber: 41, Timestamp: 12000, Marker: true}, Payload: small})
	if !ok || len(frame) == 0 {
		t.Fatalf("frame after an oversized one: ok=%v len=%d", ok, len(frame))
	}
}

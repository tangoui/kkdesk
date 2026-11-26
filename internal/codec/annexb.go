package codec

import (
	"errors"
	"time"
)

// This file holds the platform-independent parts of the Media Foundation
// encoder (encode_windows.go): converting MF output to Annex-B, keeping
// SPS/PPS in front of keyframes, and mapping sample times back to capture
// times. It has no build tags so it is unit-tested on every platform.

// H.264 NAL unit types used here.
const (
	nalSlice = 1
	nalIDR   = 5
	nalSPS   = 7
	nalPPS   = 8
)

var startCode4 = []byte{0, 0, 0, 1}

// normalizeAnnexB converts one access unit from a Media Foundation encoder
// to Annex-B with 4-byte start codes. MF encoders normally emit Annex-B
// already (3- or 4-byte start codes); some emit 4-byte big-endian length
// prefixes (AVCC) instead, which are detected by their lengths adding up
// exactly to the buffer size.
func normalizeAnnexB(b []byte) ([]byte, error) {
	if len(b) < 4 {
		return nil, errors.New("codec: access unit too short")
	}
	var nals [][]byte
	switch {
	case b[0] == 0 && b[1] == 0 && b[2] == 0 && b[3] == 1:
		nals = splitAnnexB(b)
	case b[0] == 0 && b[1] == 0 && b[2] == 1:
		// A 3-byte start code, or a length prefix of 256–511 bytes.
		if l, ok := splitLengthPrefixed(b); ok {
			nals = l
		} else {
			nals = splitAnnexB(b)
		}
	default:
		l, ok := splitLengthPrefixed(b)
		if !ok {
			return nil, errors.New("codec: access unit is neither Annex-B nor length-prefixed")
		}
		nals = l
	}
	if len(nals) == 0 {
		return nil, errors.New("codec: access unit has no NAL units")
	}
	return joinAnnexB(nals), nil
}

// splitLengthPrefixed splits 4-byte big-endian length-prefixed NAL units.
// ok is false unless the lengths cover b exactly and every NAL header has
// a zero forbidden bit and a non-zero type.
func splitLengthPrefixed(b []byte) ([][]byte, bool) {
	var nals [][]byte
	for i := 0; i < len(b); {
		if len(b)-i < 5 {
			return nil, false
		}
		n := int(b[i])<<24 | int(b[i+1])<<16 | int(b[i+2])<<8 | int(b[i+3])
		i += 4
		if n <= 0 || n > len(b)-i {
			return nil, false
		}
		if h := b[i]; h&0x80 != 0 || h&0x1f == 0 {
			return nil, false
		}
		nals = append(nals, b[i:i+n])
		i += n
	}
	return nals, len(nals) > 0
}

func joinAnnexB(nals [][]byte) []byte {
	n := 0
	for _, nal := range nals {
		n += 4 + len(nal)
	}
	out := make([]byte, 0, n)
	for _, nal := range nals {
		out = append(out, startCode4...)
		out = append(out, nal...)
	}
	return out
}

func nalType(nal []byte) int {
	if len(nal) == 0 {
		return 0
	}
	return int(nal[0] & 0x1f)
}

// paramSets caches the latest SPS and PPS so that every keyframe can be
// sent with them (a viewer joining late, or recovering after loss, needs
// them in front of the IDR).
type paramSets struct {
	sps, pps []byte
}

// learn caches the SPS/PPS found in a sequence header
// (MF_MT_MPEG_SEQUENCE_HEADER, Annex-B or length-prefixed). It reports
// whether both were present.
func (p *paramSets) learn(hdr []byte) bool {
	au, err := normalizeAnnexB(hdr)
	if err != nil {
		return false
	}
	var sps, pps []byte
	for _, nal := range splitAnnexB(au) {
		switch nalType(nal) {
		case nalSPS:
			sps = nal
		case nalPPS:
			pps = nal
		}
	}
	if sps == nil || pps == nil {
		return false
	}
	p.sps = append([]byte(nil), sps...)
	p.pps = append([]byte(nil), pps...)
	return true
}

// fix takes an Annex-B (4-byte start codes) access unit, caches any SPS/PPS
// it carries, and returns it with the cached SPS/PPS inserted before the
// first slice when it is an IDR lacking them. key reports an IDR.
func (p *paramSets) fix(au []byte) (out []byte, key bool) {
	nals := splitAnnexB(au)
	hasSPS, hasPPS := false, false
	for _, nal := range nals {
		switch nalType(nal) {
		case nalSPS:
			hasSPS = true
			p.sps = append(p.sps[:0:0], nal...)
		case nalPPS:
			hasPPS = true
			p.pps = append(p.pps[:0:0], nal...)
		case nalIDR:
			key = true
		}
	}
	if !key || (hasSPS && hasPPS) || (!hasSPS && p.sps == nil) || (!hasPPS && p.pps == nil) {
		return au, key
	}
	fixed := make([][]byte, 0, len(nals)+2)
	inserted := false
	for _, nal := range nals {
		if t := nalType(nal); !inserted && t >= nalSlice && t <= nalIDR {
			if !hasSPS {
				fixed = append(fixed, p.sps)
			}
			if !hasPPS {
				fixed = append(fixed, p.pps)
			}
			inserted = true
		}
		fixed = append(fixed, nal)
	}
	return joinAnnexB(fixed), true
}

// sampleClockSize bounds the number of in-flight samples remembered; an
// encoder holds only a few frames.
const sampleClockSize = 64

// sampleClock assigns strictly increasing MF sample times (100 ns ticks
// since the first frame) and maps output sample times back to the exact
// capture time.
type sampleClock struct {
	base time.Time
	last int64
	ring []sampleStamp
	next int
}

type sampleStamp struct {
	ticks int64
	at    time.Time
}

// stamp returns the sample time for a frame captured at t.
func (c *sampleClock) stamp(t time.Time) int64 {
	if c.base.IsZero() {
		c.base = t
		c.last = -1
	}
	ticks := int64(t.Sub(c.base) / 100)
	if ticks <= c.last {
		ticks = c.last + 1
	}
	c.last = ticks
	s := sampleStamp{ticks, t}
	if len(c.ring) < sampleClockSize {
		c.ring = append(c.ring, s)
	} else {
		c.ring[c.next] = s
		c.next = (c.next + 1) % sampleClockSize
	}
	return ticks
}

// maxEncodeAge bounds how long before now a capture time may lie: an
// encoder holds only a few frames, so anything older is not this frame's.
const maxEncodeAge = 10 * time.Second

// at returns the capture time of the output sample with the given time:
// the recorded time of that sample, else base + ticks. Hardware encoders
// may return a sample time that is unset, negative or huge, and base +
// ticks then overflows to centuries ago (a latency of 272 years on the
// controller); a result outside (now − maxEncodeAge, now] is replaced by
// now, the output time, which is within the encoding latency of the truth.
func (c *sampleClock) at(ticks int64, now time.Time) time.Time {
	t, ok := time.Time{}, false
	for _, s := range c.ring {
		if s.ticks == ticks {
			t, ok = s.at, true
			break
		}
	}
	if !ok && !c.base.IsZero() && ticks >= 0 && ticks <= c.last+int64(maxEncodeAge/100) {
		t, ok = c.base.Add(time.Duration(ticks)*100), true
	}
	if !ok || t.After(now) || now.Sub(t) > maxEncodeAge {
		return now
	}
	return t
}

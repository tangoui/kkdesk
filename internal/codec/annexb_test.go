package codec

import (
	"bytes"
	"testing"
	"time"
)

var (
	tSPS = []byte{0x67, 0x42, 0xc0, 0x1f, 0xda}
	tPPS = []byte{0x68, 0xce, 0x3c, 0x80}
	tIDR = []byte{0x65, 0x88, 0x84, 0x00, 0x10}
	tP   = []byte{0x41, 0x9a, 0x02, 0x00}
	tAUD = []byte{0x09, 0xf0}
)

func annexB4(nals ...[]byte) []byte {
	var b []byte
	for _, n := range nals {
		b = append(b, 0, 0, 0, 1)
		b = append(b, n...)
	}
	return b
}

func lengthPrefixed(nals ...[]byte) []byte {
	var b []byte
	for _, n := range nals {
		l := len(n)
		b = append(b, byte(l>>24), byte(l>>16), byte(l>>8), byte(l))
		b = append(b, n...)
	}
	return b
}

func TestNormalizeAnnexB(t *testing.T) {
	want := annexB4(tSPS, tPPS, tIDR)
	// 3-byte start codes are rewritten to 4-byte ones.
	var three []byte
	for _, n := range [][]byte{tSPS, tPPS, tIDR} {
		three = append(three, 0, 0, 1)
		three = append(three, n...)
	}
	cases := []struct {
		name string
		in   []byte
	}{
		{"annexb 4-byte", annexB4(tSPS, tPPS, tIDR)},
		{"annexb 3-byte", three},
		{"length prefixed", lengthPrefixed(tSPS, tPPS, tIDR)},
	}
	for _, c := range cases {
		got, err := normalizeAnnexB(c.in)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s: got %x want %x", c.name, got, want)
		}
	}
}

func TestNormalizeAnnexBRejectsGarbage(t *testing.T) {
	for _, in := range [][]byte{nil, {0x00}, {0, 0, 0, 9, 0x65}, {0x12, 0x34, 0x56, 0x78, 0x9a}} {
		if _, err := normalizeAnnexB(in); err == nil {
			t.Errorf("normalizeAnnexB(%x): expected error", in)
		}
	}
}

func TestParamSetsPrependOnKeyframe(t *testing.T) {
	var ps paramSets
	// First IDR carries SPS/PPS: cached, unchanged, keyframe.
	au, key := ps.fix(annexB4(tAUD, tSPS, tPPS, tIDR))
	if !key || !bytes.Equal(au, annexB4(tAUD, tSPS, tPPS, tIDR)) {
		t.Fatalf("first IDR: key=%v au=%x", key, au)
	}
	// P frame: unchanged, not key.
	au, key = ps.fix(annexB4(tP))
	if key || !bytes.Equal(au, annexB4(tP)) {
		t.Fatalf("P: key=%v au=%x", key, au)
	}
	// Later IDR without parameter sets gets them prepended.
	au, key = ps.fix(annexB4(tIDR))
	if !key || !bytes.Equal(au, annexB4(tSPS, tPPS, tIDR)) {
		t.Fatalf("bare IDR: key=%v au=%x", key, au)
	}
	// IDR with only SPS gets PPS added after its SPS (before the slice).
	au, _ = ps.fix(annexB4(tSPS, tIDR))
	if !bytes.Equal(au, annexB4(tSPS, tPPS, tIDR)) {
		t.Fatalf("SPS-only IDR: au=%x", au)
	}
}

func TestParamSetsFromSequenceHeader(t *testing.T) {
	for _, hdr := range [][]byte{lengthPrefixed(tSPS, tPPS), annexB4(tSPS, tPPS)} {
		var ps paramSets
		if !ps.learn(hdr) {
			t.Fatalf("learn(%x): no parameter sets", hdr)
		}
		au, key := ps.fix(annexB4(tIDR))
		if !key || !bytes.Equal(au, annexB4(tSPS, tPPS, tIDR)) {
			t.Fatalf("learn(%x): key=%v %x", hdr, key, au)
		}
	}
	var empty paramSets
	if empty.learn([]byte{1, 2}) {
		t.Error("learn(garbage) = true")
	}
	// Without cached parameter sets the IDR passes through.
	au, key := empty.fix(annexB4(tIDR))
	if !key || !bytes.Equal(au, annexB4(tIDR)) {
		t.Fatalf("empty cache: key=%v %x", key, au)
	}
}

func TestSampleClock(t *testing.T) {
	var c sampleClock
	base := time.Unix(1_700_000_000, 123_456_789)
	t1 := c.stamp(base)
	t2 := c.stamp(base.Add(16 * time.Millisecond))
	t3 := c.stamp(base.Add(16 * time.Millisecond)) // same time: still increasing
	if !(t1 < t2 && t2 < t3) {
		t.Fatalf("not increasing: %d %d %d", t1, t2, t3)
	}
	if got := c.lookup(t2); !got.Equal(base.Add(16 * time.Millisecond)) {
		t.Errorf("lookup(t2) = %v", got)
	}
	if got := c.lookup(t1); !got.Equal(base) {
		t.Errorf("lookup(t1) = %v", got)
	}
	// Unknown sample time falls back to base + ticks.
	if got := c.lookup(t1 + 10_000); got.Sub(base) != time.Millisecond {
		t.Errorf("fallback lookup = %v", got.Sub(base))
	}
	// Ring wraps without growing.
	for i := 0; i < 1000; i++ {
		c.stamp(base.Add(time.Duration(i) * time.Second))
	}
	if len(c.ring) > sampleClockSize {
		t.Errorf("ring grew to %d", len(c.ring))
	}
}

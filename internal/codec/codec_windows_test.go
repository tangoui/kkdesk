//go:build windows && cgo

package codec

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tangoui/kkdesk/internal/winmedia"
)

const dxgiFormatNV12 = 103

type encodedAU struct {
	nal       []byte
	captureAt time.Time
	keyframe  bool
}

// encodeSolid encodes n frames of a solid (y, 128, 128) w×h picture, paced
// at 30 fps, and returns the access units in output order. Frame forceKey
// (if ≥ 0) requests a keyframe.
func encodeSolid(t *testing.T, w, h, n int, y uint8, forceKey int) []encodedAU {
	return encodeColor(t, w, h, n, y, 128, 128, forceKey)
}

// encodeColor is encodeSolid with a (y, u, v) picture.
func encodeColor(t *testing.T, w, h, n int, y, u, v uint8, forceKey int) []encodedAU {
	t.Helper()
	var mu sync.Mutex
	var outs []encodedAU
	enc, err := NewEncoder(w, h, 30, 4_000_000, func(nal []byte, captureAt time.Time, keyframe bool) {
		mu.Lock()
		outs = append(outs, encodedAU{append([]byte(nil), nal...), captureAt, keyframe})
		mu.Unlock()
	})
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	tex, err := newSolidNV12(w, h, y, u, v)
	if err != nil {
		t.Fatalf("newSolidNV12: %v", err)
	}
	defer winmedia.Release(tex)
	start := time.Now()
	for i := 0; i < n; i++ {
		if d := time.Until(start.Add(time.Duration(i) * time.Second / 30)); d > 0 {
			time.Sleep(d)
		}
		// Distinct capture times so pass-through can be checked.
		if err := enc.Encode(Frame{Pixels: tex, CaptureAt: time.Now()}, i == forceKey); err != nil {
			t.Fatalf("Encode %d: %v", i, err)
		}
	}
	enc.Close()
	mu.Lock()
	defer mu.Unlock()
	if len(outs) == 0 || !outs[0].keyframe {
		t.Fatalf("encoder produced %d frames, first keyframe=%v", len(outs), len(outs) > 0 && outs[0].keyframe)
	}
	return outs
}

type decodedPic struct {
	w, h, format, array int
	center, bottom      uint8
	captureAt           time.Time
}

// collector is a DecodeOutput that records every picture.
type collector struct {
	t    *testing.T
	mu   sync.Mutex
	pics []decodedPic
}

func (c *collector) out(pix uintptr, captureAt time.Time) {
	w, h, f, a := textureInfo(pix)
	p := decodedPic{w: w, h: h, format: f, array: a, captureAt: captureAt}
	var err error
	if p.center, err = lumaAt(pix, w/2, h/2); err != nil {
		c.t.Errorf("lumaAt center: %v", err)
	}
	if p.bottom, err = lumaAt(pix, w/2, h-1); err != nil {
		c.t.Errorf("lumaAt bottom: %v", err)
	}
	c.mu.Lock()
	c.pics = append(c.pics, p)
	c.mu.Unlock()
}

func (c *collector) snapshot() []decodedPic {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]decodedPic(nil), c.pics...)
}

func near(v, want uint8) bool { return int(v) >= int(want)-4 && int(v) <= int(want)+4 }

func decodeAll(t *testing.T, dec Decoder, aus []encodedAU) {
	t.Helper()
	for i, au := range aus {
		if err := dec.Decode(au.nal, au.captureAt); err != nil {
			t.Fatalf("Decode %d: %v", i, err)
		}
	}
}

func checkSolid1080p(t *testing.T) {
	const w, h = 1920, 1080
	aus := encodeSolid(t, w, h, 30, 180, -1)
	if len(aus) < 25 {
		t.Fatalf("encoder produced only %d of 30 frames", len(aus))
	}
	c := &collector{t: t}
	dec, err := NewDecoder(c.out)
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	defer dec.Close()
	if in, ok := dec.(interface{ Info() string }); ok {
		t.Logf("decoder: %s", in.Info())
	}
	decodeAll(t, dec, aus)
	pics := c.snapshot()
	if len(pics) != len(aus) {
		t.Fatalf("decoded %d pictures from %d access units", len(pics), len(aus))
	}
	for i, p := range pics {
		if p.w != w || p.h != h || p.format != dxgiFormatNV12 || p.array != 1 {
			t.Fatalf("picture %d: %dx%d format %d array %d, want %dx%d NV12 array 1", i, p.w, p.h, p.format, p.array, w, h)
		}
		if !near(p.center, 180) || !near(p.bottom, 180) {
			t.Errorf("picture %d: Y center %d bottom %d, want ≈180", i, p.center, p.bottom)
		}
		if !p.captureAt.Equal(aus[i].captureAt) {
			t.Errorf("picture %d: captureAt %v, want %v", i, p.captureAt, aus[i].captureAt)
		}
	}
}

func TestDecode1080pCropped(t *testing.T) { checkSolid1080p(t) }

func TestDecodeSoftware(t *testing.T) {
	t.Setenv("KKDESK_DECODER", "software")
	c := &collector{t: t}
	dec, err := NewDecoder(c.out)
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	info := dec.(interface{ Info() string }).Info()
	dec.Close()
	if !strings.Contains(info, "software") {
		t.Fatalf("Info() = %q, want it to mention software", info)
	}
	checkSolid1080p(t)
}

func TestDecodeResolutionChange(t *testing.T) {
	first := encodeSolid(t, 1280, 720, 10, 100, -1)
	second := encodeSolid(t, 640, 360, 10, 200, -1)
	c := &collector{t: t}
	dec, err := NewDecoder(c.out)
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	defer dec.Close()
	decodeAll(t, dec, first)
	n := len(c.snapshot())
	decodeAll(t, dec, second)
	pics := c.snapshot()
	for i, p := range pics[:n] {
		if p.w != 1280 || p.h != 720 {
			t.Errorf("picture %d: %dx%d, want 1280x720", i, p.w, p.h)
		}
	}
	small := 0
	for _, p := range pics[n:] {
		if p.w == 640 && p.h == 360 && near(p.center, 200) {
			small++
		}
	}
	if small < len(second)-2 {
		t.Fatalf("%d pictures at 640x360 after the change, want ≥ %d", small, len(second)-2)
	}
}

func TestDecodeRecoversAfterCorruptFrame(t *testing.T) {
	aus := encodeSolid(t, 1280, 720, 16, 120, 10)
	if !aus[10].keyframe {
		t.Skipf("encoder did not honour forceKey at frame 10 (dropped frames: %d outputs)", len(aus))
	}
	c := &collector{t: t}
	dec, err := NewDecoder(c.out)
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	defer dec.Close()
	decodeAll(t, dec, aus[:5])
	done := make(chan error, 1)
	go func() {
		bad := aus[5].nal
		done <- dec.Decode(bad[:len(bad)/2], aus[5].captureAt)
	}()
	select {
	case err := <-done:
		t.Logf("truncated frame: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("Decode of a truncated frame blocked for 2s")
	}
	before := len(c.snapshot())
	decodeAll(t, dec, aus[10:])
	if got := len(c.snapshot()) - before; got < len(aus)-10 {
		t.Fatalf("decoded %d pictures after the keyframe, want %d", got, len(aus)-10)
	}
}

func TestDecodeSliceBeforeSPS(t *testing.T) {
	aus := encodeSolid(t, 640, 360, 3, 90, -1)
	c := &collector{t: t}
	dec, err := NewDecoder(c.out)
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	defer dec.Close()
	if err := dec.Decode(aus[1].nal, aus[1].captureAt); err == nil {
		t.Fatal("Decode of a P frame before any SPS/PPS returned nil")
	}
	if n := len(c.snapshot()); n != 0 {
		t.Fatalf("%d pictures delivered for a slice before SPS/PPS", n)
	}
}

func TestDecodeAfterClose(t *testing.T) {
	dec, err := NewDecoder(func(uintptr, time.Time) {})
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	dec.Close()
	dec.Close()
	if err := dec.Decode([]byte{0, 0, 0, 1, 0x65, 0x88}, time.Now()); err == nil {
		t.Fatal("Decode after Close returned nil")
	}
}

// checkChroma decodes a solid (150, 90, 200) 1920x1080 picture and checks
// U and V at the center and on the first and last chroma rows.
func checkChroma(t *testing.T) {
	aus := encodeColor(t, 1920, 1080, 10, 150, 90, 200, -1)
	var mu sync.Mutex
	var got [][6]uint8
	dec, err := NewDecoder(func(pix uintptr, _ time.Time) {
		u, v, err := chromaAt(pix, 960, 540)
		if err != nil {
			t.Errorf("chromaAt center: %v", err)
		}
		bu, bv, err := chromaAt(pix, 960, 1079)
		if err != nil {
			t.Errorf("chromaAt bottom: %v", err)
		}
		tu, tv, err := chromaAt(pix, 960, 0)
		if err != nil {
			t.Errorf("chromaAt top: %v", err)
		}
		mu.Lock()
		got = append(got, [6]uint8{u, v, bu, bv, tu, tv})
		mu.Unlock()
	})
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	defer dec.Close()
	decodeAll(t, dec, aus)
	mu.Lock()
	defer mu.Unlock()
	if len(got) == 0 {
		t.Fatal("no pictures decoded")
	}
	near6 := func(a, b uint8) bool { return int(a) >= int(b)-6 && int(a) <= int(b)+6 }
	for i, c := range got {
		if !near6(c[0], 90) || !near6(c[1], 200) || !near6(c[2], 90) || !near6(c[3], 200) || !near6(c[4], 90) || !near6(c[5], 200) {
			t.Fatalf("picture %d: UV center %d,%d bottom %d,%d top %d,%d, want ≈90,200", i, c[0], c[1], c[2], c[3], c[4], c[5])
		}
	}
}

func TestDecodeChroma(t *testing.T) { checkChroma(t) }

func TestDecodeChromaSoftware(t *testing.T) {
	t.Setenv("KKDESK_DECODER", "software")
	checkChroma(t)
}

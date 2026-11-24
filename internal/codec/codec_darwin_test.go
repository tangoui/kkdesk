//go:build darwin && cgo

package codec

import (
	"sort"
	"sync"
	"testing"
	"time"
)

type encOut struct {
	nal       []byte
	captureAt time.Time
	keyframe  bool
	at        time.Time
}

// nalTypes returns the NAL unit types of an Annex-B buffer, in order.
func nalTypes(b []byte) []int {
	var t []int
	for _, n := range splitAnnexB(b) {
		if len(n) > 0 {
			t = append(t, int(n[0]&0x1f))
		}
	}
	return t
}

func median(d []time.Duration) time.Duration {
	if len(d) == 0 {
		return 0
	}
	s := append([]time.Duration(nil), d...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	return s[len(s)/2]
}

func TestEncodeDecode1080p(t *testing.T) {
	const (
		w, h   = 1920, 1080
		fps    = 60
		bps    = 8_000_000
		frames = 60
		forced = 30
	)

	var mu sync.Mutex
	var outs []encOut
	enc, err := NewEncoder(w, h, fps, bps, func(nal []byte, captureAt time.Time, keyframe bool) {
		now := time.Now()
		mu.Lock()
		outs = append(outs, encOut{append([]byte(nil), nal...), captureAt, keyframe, now})
		mu.Unlock()
	})
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	if in, ok := enc.(interface{ Info() string }); ok {
		t.Logf("encoder: %s", in.Info())
	}

	start := time.Now()
	captures := make([]time.Time, frames)
	for i := 0; i < frames; i++ {
		// Pace like a real 60 fps source so rate control behaves normally.
		next := start.Add(time.Duration(i) * time.Second / fps)
		if d := time.Until(next); d > 0 {
			time.Sleep(d)
		}
		// A fresh buffer per frame: VT may still hold the previous one.
		p, err := newNV12PixelBuffer(w, h)
		if err != nil {
			t.Fatalf("newNV12PixelBuffer: %v", err)
		}
		fillTestPattern(p, i)
		captures[i] = time.Now()
		if err := enc.Encode(Frame{Pixels: p, CaptureAt: captures[i]}, i == forced); err != nil {
			t.Fatalf("Encode %d: %v", i, err)
		}
		releasePixelBuffer(p)
	}
	enc.Close() // flushes pending frames

	mu.Lock()
	defer mu.Unlock()
	if len(outs) != frames {
		t.Fatalf("encoded %d frames, want %d", len(outs), frames)
	}

	var encLat []time.Duration
	keys := 0
	for i, o := range outs {
		if !o.captureAt.Equal(captures[i]) {
			t.Errorf("frame %d captureAt %v, want %v", i, o.captureAt, captures[i])
		}
		encLat = append(encLat, o.at.Sub(o.captureAt))
		ty := nalTypes(o.nal)
		if o.keyframe {
			keys++
			if len(ty) < 3 || ty[0] != 7 || ty[1] != 8 {
				t.Errorf("keyframe %d NAL types %v, want SPS, PPS, ...", i, ty)
			}
		}
	}
	if !outs[0].keyframe {
		t.Errorf("first output is not a keyframe")
	}
	if !outs[forced].keyframe {
		t.Errorf("frame %d (forceKey) is not a keyframe", forced)
	}
	if outs[1].keyframe {
		t.Errorf("frame 1 is unexpectedly a keyframe")
	}

	var dmu sync.Mutex
	var decLat []time.Duration
	decoded := 0
	var decStart time.Time
	dec, err := NewDecoder(func(p uintptr, captureAt time.Time) {
		dw, dh := pixelBufferSize(p)
		dmu.Lock()
		defer dmu.Unlock()
		if dw != w || dh != h {
			t.Errorf("decoded size %dx%d, want %dx%d", dw, dh, w, h)
		}
		decLat = append(decLat, time.Since(decStart))
		decoded++
	})
	if err != nil {
		t.Fatalf("NewDecoder: %v", err)
	}
	for i, o := range outs {
		decStart = time.Now()
		if err := dec.Decode(o.nal, o.captureAt); err != nil {
			t.Fatalf("Decode %d: %v", i, err)
		}
	}
	dec.Close()
	dmu.Lock()
	defer dmu.Unlock()
	if decoded != frames {
		t.Fatalf("decoded %d frames, want %d", decoded, frames)
	}
	t.Logf("frames=%d keyframes=%d bytes(first)=%d median encode latency=%v median decode latency=%v",
		len(outs), keys, len(outs[0].nal), median(encLat), median(decLat))
}

func TestSplitAnnexB(t *testing.T) {
	b := []byte{0, 0, 0, 1, 0x67, 1, 2, 0, 0, 1, 0x68, 3, 0, 0, 0, 1, 0x65, 4, 5}
	got := splitAnnexB(b)
	if len(got) != 3 || got[0][0] != 0x67 || len(got[0]) != 3 || got[1][0] != 0x68 || len(got[1]) != 2 || len(got[2]) != 3 {
		t.Fatalf("splitAnnexB = %x", got)
	}
}

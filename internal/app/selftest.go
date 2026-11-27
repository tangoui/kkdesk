package app

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/tangoui/kkdesk/internal/capture"
	"github.com/tangoui/kkdesk/internal/codec"
	"github.com/tangoui/kkdesk/internal/inject"
	"github.com/tangoui/kkdesk/internal/winmedia"
)

// SelftestConfig configures Selftest.
type SelftestConfig struct {
	FPS         int    // capture and encode rate; default 60
	MaxWidth    int    // capture width limit; default 1920
	BitrateMbps int    // 0 = AutoBitrate
	Dir         string // where kkdesk-selftest.log and selftest.h264 go; default OutputDir()
}

// Selftest file names.
const (
	SelftestLog  = "kkdesk-selftest.log"
	SelftestH264 = "selftest.h264"
)

const (
	selftestCapture = 3 * time.Second
	selftestFrames  = 60
)

// Selftest checks the host pipeline without UI or network: system
// information, 3 s of screen capture, encoding 60 frames (keyframes forced
// at 0 and 30, written to selftest.h264) and creating the input injector
// (no input is sent). Everything is printed to stdout and written to
// kkdesk-selftest.log. It reports whether every check passed.
func Selftest(cfg SelftestConfig) bool {
	if cfg.FPS <= 0 {
		cfg.FPS = 60
	}
	if cfg.MaxWidth == 0 {
		cfg.MaxWidth = 1920
	}
	if cfg.Dir == "" {
		cfg.Dir = OutputDir()
	}
	bps := AutoBitrate
	if cfg.BitrateMbps > 0 {
		bps = cfg.BitrateMbps * 1_000_000
	}

	var w io.Writer = os.Stdout
	logPath := filepath.Join(cfg.Dir, SelftestLog)
	if f, err := os.Create(logPath); err == nil {
		defer f.Close()
		w = fileFirst{file: f, console: os.Stdout} // no usable stdout in a GUI build
	} else {
		fmt.Fprintf(os.Stderr, "kkdesk: cannot write %s: %v (output on screen only)\n", logPath, err)
	}
	var mu sync.Mutex
	p := func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		fmt.Fprintf(w, time.Now().Format("15:04:05.000 ")+format+"\n", args...)
	}
	// Backend diagnostics (winmedia on Windows, stray log.Printf) go to
	// the same output.
	winmedia.SetLogger(func(s string) { p("  win: %s", s) })
	defer winmedia.SetLogger(nil)
	log.SetFlags(0)
	log.SetOutput(writerFunc(func(b []byte) (int, error) { p("  log: %s", trimNL(string(b))); return len(b), nil }))
	defer func() { log.SetOutput(os.Stderr); log.SetFlags(log.LstdFlags) }()

	p("== kkdesk self-test ==")
	p("log file: %s", logPath)
	p("-- system --")
	for _, l := range SystemInfo() {
		p("%s", l)
	}

	results := map[string]bool{}
	results["capture"] = selftestCapture3s(p, cfg)
	results["encode"] = selftestEncode(p, cfg, bps)
	results["inject"] = selftestInject(p)

	p("-- result --")
	ok := true
	for _, k := range []string{"capture", "encode", "inject"} {
		s := "PASS"
		if !results[k] {
			s, ok = "FAIL", false
		}
		p("%s: %s", k, s)
	}
	if ok {
		p("SELFTEST PASS")
	} else {
		p("SELFTEST FAIL")
	}
	p("please send back: %s and %s", logPath, filepath.Join(cfg.Dir, SelftestH264))
	return ok
}

func selftestCapture3s(p func(string, ...any), cfg SelftestConfig) bool {
	p("-- capture: %v at %d fps, max width %d --", selftestCapture, cfg.FPS, cfg.MaxWidth)
	if help := CaptureHelp(); help != "" {
		p("capture: not permitted: %s", help)
		return false
	}
	var (
		mu       sync.Mutex
		n        int
		first    time.Time
		last     time.Time
		fw, fh   int
		sizes    = map[[2]int]int{}
		maxGap   time.Duration
		captureL []time.Duration // ArrivedAt - CaptureAt
	)
	start := time.Now()
	s, err := capture.Start(capture.Options{FPS: cfg.FPS, MaxWidth: cfg.MaxWidth}, func(f capture.Frame) {
		now := time.Now()
		mu.Lock()
		defer mu.Unlock()
		if n == 0 {
			first, fw, fh = now, f.Width, f.Height
		} else if g := now.Sub(last); g > maxGap {
			maxGap = g
		}
		last = now
		n++
		sizes[[2]int{f.Width, f.Height}]++
		if !f.ArrivedAt.IsZero() && !f.CaptureAt.IsZero() {
			captureL = append(captureL, f.ArrivedAt.Sub(f.CaptureAt))
		}
	})
	if err != nil {
		p("capture: Start failed after %v: %v", time.Since(start).Round(time.Millisecond), err)
		return false
	}
	p("capture: started in %v, output size %v", time.Since(start).Round(time.Millisecond), sizeOf(s))
	time.Sleep(selftestCapture)
	st := s.Stats()
	s.Close()

	mu.Lock()
	defer mu.Unlock()
	p("capture: %d callbacks (complete %d, resent %d), idle %d, other %d, late %d, reopened %d",
		n, st.Complete, st.Resent, st.Idle, st.Other, st.Late, st.Reopened)
	if n > 0 {
		p("capture: first frame %dx%d after %v; sizes %v; max gap between callbacks %v",
			fw, fh, first.Sub(start).Round(time.Millisecond), sizes, maxGap.Round(time.Millisecond))
	}
	p("capture: new-image rate %.1f fps (a static screen produces few new images; move a window or play a video for a real rate)",
		float64(st.Complete)/selftestCapture.Seconds())
	if st.Complete > 0 && st.AcquireTotal > 0 {
		p("capture: AcquireTotal %v, average AcquireNextFrame %s per new image", st.AcquireTotal.Round(time.Microsecond),
			ms(st.AcquireTotal/time.Duration(st.Complete)))
	}
	if len(captureL) > 0 {
		m, p95 := percentiles(captureL)
		p("capture: present→callback median %s p95 %s", ms(m), ms(p95))
	}
	if n == 0 {
		p("capture: FAIL: no frame delivered")
		return false
	}
	return true
}

func selftestEncode(p func(string, ...any), cfg SelftestConfig, bps int) bool {
	p("-- encode: %d frames at %d fps, %.1f Mbps, keyframes forced at 0 and 30 --", selftestFrames, cfg.FPS, float64(bps)/1e6)
	if CaptureHelp() != "" {
		p("encode: skipped (no capture permission)")
		return false
	}
	h264Path := filepath.Join(cfg.Dir, SelftestH264)
	h264, err := os.Create(h264Path)
	if err != nil {
		p("encode: cannot create %s: %v", h264Path, err)
		return false
	}
	defer h264.Close()

	var (
		mu        sync.Mutex
		submitted = map[int64]time.Time{} // CaptureAt UnixNano → Encode call time
		lat       []time.Duration
		outputs   int
		keys      []int // output index of keyframes
		keySPS    int   // keyframes carrying SPS and PPS
		bytes     int
		keyBytes  int
		nalCounts = map[int]int{}
		writeErr  error
		encInfo   string
		encErrs   int
		encErr    error
		w, h      int
	)
	out := func(nal []byte, captureAt time.Time, key bool) {
		now := time.Now()
		mu.Lock()
		defer mu.Unlock()
		if t, ok := submitted[captureAt.UnixNano()]; ok {
			lat = append(lat, now.Sub(t))
			delete(submitted, captureAt.UnixNano())
		}
		types := annexBTypes(nal)
		for _, t := range types {
			nalCounts[t]++
		}
		if key {
			keys = append(keys, outputs)
			keyBytes += len(nal)
			if hasType(types, 7) && hasType(types, 8) {
				keySPS++
			}
		}
		outputs++
		bytes += len(nal)
		if _, err := h264.Write(nal); err != nil && writeErr == nil {
			writeErr = err
		}
	}

	var enc codec.Encoder
	var newErr error
	done := make(chan struct{})
	var once sync.Once
	frameDur := time.Second / time.Duration(cfg.FPS)
	s, err := capture.Start(capture.Options{FPS: cfg.FPS, MaxWidth: cfg.MaxWidth}, func(f capture.Frame) {
		once.Do(func() {
			defer close(done)
			w, h = f.Width, f.Height
			t0 := time.Now()
			e, err := codec.NewEncoder(f.Width, f.Height, cfg.FPS, bps, out)
			if err != nil {
				newErr = err
				return
			}
			enc = e
			encInfo = EncoderInfo(e)
			p("encode: encoder for %dx%d created in %v: %s", f.Width, f.Height, time.Since(t0).Round(time.Millisecond), encInfo)
			// The frame is only valid during this callback, so the same
			// image is encoded 60 times, paced in real time: keyframes are
			// representative, P-frames of a static image are tiny.
			base := time.Now()
			for i := range selftestFrames {
				capAt := base.Add(time.Duration(i) * frameDur)
				mu.Lock()
				submitted[capAt.UnixNano()] = time.Now()
				mu.Unlock()
				if err := e.Encode(codec.Frame{Pixels: f.Pixels, CaptureAt: capAt}, i == 0 || i == 30); err != nil {
					mu.Lock()
					encErrs++
					if encErr == nil {
						encErr = fmt.Errorf("frame %d: %w", i, err)
					}
					mu.Unlock()
				}
				if d := time.Until(base.Add(time.Duration(i+1) * frameDur)); d > 0 {
					time.Sleep(d)
				}
			}
		})
	})
	if err != nil {
		p("encode: capture Start failed: %v", err)
		return false
	}
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		p("encode: FAIL: no captured frame within 15 s")
		s.Close()
		return false
	}
	s.Close()
	if newErr != nil {
		p("encode: FAIL: NewEncoder(%dx%d): %v", w, h, newErr)
		return false
	}
	t0 := time.Now()
	enc.Close() // flushes the remaining output
	p("encode: Close (flush) took %v", time.Since(t0).Round(time.Millisecond))

	mu.Lock()
	defer mu.Unlock()
	p("encode: encoder: %s", encInfo)
	p("encode: %d outputs for %d frames, %d Encode errors%s", outputs, selftestFrames, encErrs, errSuffix(encErr))
	if len(lat) > 0 {
		m, p95 := percentiles(lat)
		p("encode: Encode→output latency median %s p95 %s max %s (%d samples); first output %s (includes warm-up)",
			ms(m), ms(p95), ms(maxDur(lat)), len(lat), ms(lat[0]))
		if len(lat) > 2 {
			m, p95 = percentiles(lat[1:])
			p("encode: excluding the first output: median %s p95 %s", ms(m), ms(p95))
		}
	}
	secs := float64(selftestFrames) / float64(cfg.FPS)
	p("encode: %d bytes total, %.2f Mbps over %.1f s of video (static image: P-frames are tiny), keyframes %d at outputs %v, "+
		"%d bytes in keyframes, keyframes with SPS+PPS %d", bytes, float64(bytes*8)/secs/1e6, secs, len(keys), keys, keyBytes, keySPS)
	p("encode: NAL unit types %v (1 = slice, 5 = IDR, 6 = SEI, 7 = SPS, 8 = PPS, 9 = AUD)", nalCounts)
	if writeErr != nil {
		p("encode: writing %s failed: %v", h264Path, writeErr)
	} else {
		p("encode: wrote %s", h264Path)
	}
	ok := outputs > 0 && len(keys) > 0 && keySPS == len(keys) && writeErr == nil
	if len(keys) < 2 {
		p("encode: WARNING: expected 2 keyframes (forced at 0 and 30), got %d", len(keys))
	}
	if !ok {
		p("encode: FAIL (need output, keyframes, SPS/PPS on every keyframe)")
	}
	return ok
}

func selftestInject(p func(string, ...any)) bool {
	p("-- inject (no input is sent) --")
	if help := InjectHelp(); help != "" {
		p("inject: %s", help)
	}
	if lim := InjectLimits(); lim != "" {
		p("inject: %s", lim)
	}
	in, err := inject.New()
	if err != nil {
		p("inject: FAIL: New: %v", err)
		return false
	}
	b := in.Bounds()
	p("inject: primary display bounds %.0fx%.0f at (%.0f,%.0f)", b.W, b.H, b.X, b.Y)
	return true
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(b []byte) (int, error) { return f(b) }

func trimNL(s string) string {
	for len(s) > 0 && s[len(s)-1] == '\n' {
		s = s[:len(s)-1]
	}
	return s
}

func sizeOf(s capture.Session) string {
	w, h := s.Size()
	return fmt.Sprintf("%dx%d", w, h)
}

func errSuffix(err error) string {
	if err == nil {
		return ""
	}
	return " (first: " + err.Error() + ")"
}

// percentiles returns the median and the 95th percentile.
func percentiles(d []time.Duration) (median, p95 time.Duration) {
	s := append([]time.Duration(nil), d...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	return s[len(s)/2], s[min(len(s)-1, len(s)*95/100)]
}

func maxDur(d []time.Duration) time.Duration {
	var m time.Duration
	for _, v := range d {
		m = max(m, v)
	}
	return m
}

// annexBTypes returns the NAL unit types in an Annex-B buffer.
func annexBTypes(b []byte) []int {
	var t []int
	for i := 0; i+3 < len(b); i++ {
		if b[i] == 0 && b[i+1] == 0 && b[i+2] == 1 {
			t = append(t, int(b[i+3]&0x1f))
			i += 3
		}
	}
	return t
}

func hasType(ts []int, t int) bool {
	for _, v := range ts {
		if v == t {
			return true
		}
	}
	return false
}

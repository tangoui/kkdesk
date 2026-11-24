package stats

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)

func ms(n int) time.Duration { return time.Duration(n) * time.Millisecond }

// viewerFrames returns n viewer frames whose capture→presented latency is
// base, base+1, ... ms, split into received/decoded/enqueued/presented with
// fixed 2/3/1 ms tail segments.
func viewerFrames(n, base int, start time.Time) []FrameTimes {
	var fs []FrameTimes
	for i := 0; i < n; i++ {
		c := start.Add(time.Duration(i) * 10 * time.Millisecond)
		lat := ms(base + i)
		p := c.Add(lat)
		fs = append(fs, FrameTimes{
			Seq:       uint64(i),
			Capture:   c,
			Received:  p.Add(-ms(6)),
			Decoded:   p.Add(-ms(4)),
			Enqueued:  p.Add(-ms(1)),
			Presented: p,
		})
	}
	return fs
}

func TestPercentile(t *testing.T) {
	var s []time.Duration
	for i := 10; i < 110; i++ {
		s = append(s, ms(i))
	}
	if got := percentile(s, 50); got != ms(59) {
		t.Fatalf("p50 = %v, want 59ms", got)
	}
	if got := percentile(s, 95); got != ms(104) {
		t.Fatalf("p95 = %v, want 104ms", got)
	}
	if got := percentile(nil, 50); got != 0 {
		t.Fatalf("empty = %v", got)
	}
}

func TestFlushViewerSecond(t *testing.T) {
	var buf bytes.Buffer
	cpu := func() map[string]float64 { return map[string]float64{"self": 12.5, "WindowServer": 3} }
	r := newRecorderAt(&buf, "viewer", cpu, t0)
	// Shuffle order to make sure sorting is done.
	fs := viewerFrames(100, 10, t0)
	for i := len(fs) - 1; i >= 0; i-- {
		r.Frame(fs[i])
	}
	r.AddBytes(500_000)
	r.AddBytes(500_000)
	r.Loss(1.5)

	sec, err := r.Flush(t0.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if sec.FPS != 100 {
		t.Errorf("fps = %v", sec.FPS)
	}
	if sec.BitrateBps != 8_000_000 {
		t.Errorf("bitrate = %v", sec.BitrateBps)
	}
	if sec.LossPct != 1.5 {
		t.Errorf("loss = %v", sec.LossPct)
	}
	if sec.LatencyP50 != ms(59) || sec.LatencyP95 != ms(104) {
		t.Errorf("p50/p95 = %v/%v", sec.LatencyP50, sec.LatencyP95)
	}
	want := map[string]time.Duration{
		SegCaptureReceived:   ms(59 - 6),
		SegReceivedDecoded:   ms(2),
		SegDecodedEnqueued:   ms(3),
		SegEnqueuedPresented: ms(1),
	}
	if len(sec.Segments) != len(want) {
		t.Errorf("segments = %v", sec.Segments)
	}
	for k, v := range want {
		if sec.Segments[k] != v {
			t.Errorf("segment %s = %v, want %v", k, sec.Segments[k], v)
		}
	}

	line := strings.TrimSpace(buf.String())
	if strings.Count(line, "\n") != 0 {
		t.Fatalf("want one line, got %q", line)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		t.Fatalf("invalid json: %v: %s", err, line)
	}
	for _, k := range []string{"time", "role", "fps", "bitrate_bps", "loss_pct", "latency_p50_ms", "latency_p95_ms", "segments_ms", "cpu", "lat_ms", "seg_samples_ms"} {
		if _, ok := m[k]; !ok {
			t.Errorf("missing field %q in %s", k, line)
		}
	}
	if m["role"] != "viewer" || m["latency_p50_ms"] != 59.0 || m["latency_p95_ms"] != 104.0 {
		t.Errorf("bad fields: %s", line)
	}
	if seg := m["segments_ms"].(map[string]any); seg["received_decoded"] != 2.0 {
		t.Errorf("segments_ms = %v", seg)
	}
	if c := m["cpu"].(map[string]any); c["self"] != 12.5 {
		t.Errorf("cpu = %v", c)
	}
	if n := len(m["lat_ms"].([]any)); n != 100 {
		t.Errorf("lat_ms len = %d", n)
	}

	// Next second has no frames: counters are reset, loss is sticky.
	buf.Reset()
	sec, err = r.Flush(t0.Add(2 * time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if sec.FPS != 0 || sec.BitrateBps != 0 || sec.LatencyP50 != 0 || len(sec.Segments) != 0 {
		t.Errorf("second 2 not reset: %+v", sec)
	}
	if sec.LossPct != 1.5 {
		t.Errorf("loss not sticky: %v", sec.LossPct)
	}
}

func TestFlushHostSecond(t *testing.T) {
	var buf bytes.Buffer
	r := newRecorderAt(&buf, "host", nil, t0)
	for i := 0; i < 30; i++ {
		c := t0.Add(time.Duration(i) * 33 * time.Millisecond)
		r.Frame(FrameTimes{Seq: uint64(i), Capture: c, Encoded: c.Add(ms(4 + i%3)), Sent: c.Add(ms(5 + i%3))})
	}
	sec, err := r.Flush(t0.Add(500 * time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	if sec.FPS != 60 {
		t.Errorf("fps over half second = %v", sec.FPS)
	}
	if sec.LatencyP50 != ms(6) || sec.LatencyP95 != ms(7) {
		t.Errorf("host p50/p95 = %v/%v", sec.LatencyP50, sec.LatencyP95)
	}
	if sec.Segments[SegCaptureEncoded] != ms(5) || sec.Segments[SegEncodedSent] != ms(1) || len(sec.Segments) != 2 {
		t.Errorf("host segments = %v", sec.Segments)
	}
	if strings.Contains(buf.String(), `"cpu"`) {
		t.Errorf("cpu should be omitted without sampler: %s", buf.String())
	}
}

func TestRecorderReportAndCap(t *testing.T) {
	r := newRecorderAt(&bytes.Buffer{}, "viewer", nil, t0)
	r.maxSamples = 50
	for _, f := range viewerFrames(100, 10, t0) {
		r.Frame(f)
	}
	if _, err := r.Flush(t0.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	rep := r.Report()
	if rep.Frames != 100 || rep.Seconds != 1 {
		t.Errorf("frames/seconds = %d/%d", rep.Frames, rep.Seconds)
	}
	// Only the first 50 samples (10..59ms) are kept.
	if rep.LatencyP50 != ms(34) {
		t.Errorf("capped p50 = %v", rep.LatencyP50)
	}
}

func TestSummary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log.jsonl")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	cpuVals := []float64{10, 30}
	i := 0
	cpu := func() map[string]float64 {
		v := cpuVals[i]
		i++
		return map[string]float64{"self": v, "VTDecoderXPCService": v / 10}
	}
	r := newRecorderAt(f, "viewer", cpu, t0)
	// Second 1: 60 frames, latencies 10..69. Second 2: 40 frames, 70..109.
	all := viewerFrames(100, 10, t0)
	for _, fr := range all[:60] {
		r.Frame(fr)
	}
	r.AddBytes(1_000_000)
	if _, err := r.Flush(t0.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	for _, fr := range all[60:] {
		r.Frame(fr)
	}
	r.AddBytes(500_000)
	r.Loss(2)
	if _, err := r.Flush(t0.Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	rep, err := Summary(path)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Role != "viewer" || rep.Seconds != 2 || rep.Frames != 100 {
		t.Errorf("role/seconds/frames = %q/%d/%d", rep.Role, rep.Seconds, rep.Frames)
	}
	if rep.LatencyP50 != ms(59) || rep.LatencyP95 != ms(104) {
		t.Errorf("overall p50/p95 = %v/%v", rep.LatencyP50, rep.LatencyP95)
	}
	if rep.MeanFPS != 50 {
		t.Errorf("mean fps = %v", rep.MeanFPS)
	}
	if rep.MeanBitrateBps != 6_000_000 {
		t.Errorf("mean bitrate = %v", rep.MeanBitrateBps)
	}
	if rep.MeanLossPct != 1 {
		t.Errorf("mean loss = %v", rep.MeanLossPct)
	}
	if rep.Segments[SegReceivedDecoded] != ms(2) || rep.Segments[SegCaptureReceived] != ms(53) {
		t.Errorf("segments = %v", rep.Segments)
	}
	if rep.CPU["self"] != 20 || rep.CPU["VTDecoderXPCService"] != 2 {
		t.Errorf("cpu = %v", rep.CPU)
	}

	// The in-memory report agrees with the file.
	mem := r.Report()
	if mem.LatencyP50 != rep.LatencyP50 || mem.LatencyP95 != rep.LatencyP95 || mem.MeanFPS != rep.MeanFPS || mem.CPU["self"] != 20 {
		t.Errorf("memory report %+v != file report %+v", mem, rep)
	}
}

func TestSummaryErrors(t *testing.T) {
	if _, err := Summary(filepath.Join(t.TempDir(), "missing.jsonl")); err == nil {
		t.Error("missing file: want error")
	}
	path := filepath.Join(t.TempDir(), "bad.jsonl")
	os.WriteFile(path, []byte("{not json}\n"), 0o644)
	if _, err := Summary(path); err == nil {
		t.Error("bad json: want error")
	}
}

func TestStartClose(t *testing.T) {
	var buf syncBuffer
	r := NewRecorder(&buf, "host", nil)
	r.Start()
	c := time.Now()
	r.Frame(FrameTimes{Capture: c, Encoded: c.Add(ms(1)), Sent: c.Add(ms(2))})
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	// Close flushes the pending frame.
	if !strings.Contains(buf.String(), `"role":"host"`) {
		t.Errorf("no line after Close: %q", buf.String())
	}
	if err := r.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

func TestParseCPUTime(t *testing.T) {
	cases := map[string]float64{
		"0:00.00":     0,
		"1:02.34":     62.34,
		"123:45.67":   123*60 + 45.67,
		"02:03:04":    2*3600 + 3*60 + 4,
		"1-02:03:04":  86400 + 2*3600 + 3*60 + 4,
		"12-00:00:01": 12*86400 + 1,
	}
	for in, want := range cases {
		got, err := parseCPUTime(in)
		if err != nil {
			t.Errorf("%q: %v", in, err)
			continue
		}
		if d := got - want; d > 1e-9 || d < -1e-9 {
			t.Errorf("%q = %v, want %v", in, got, want)
		}
	}
	for _, bad := range []string{"", "abc", "1:xx", "x-01:00:00"} {
		if _, err := parseCPUTime(bad); err == nil {
			t.Errorf("%q: want error", bad)
		}
	}
}

func TestParsePS(t *testing.T) {
	out := `  1 /sbin/launchd       1:02.34
  357 /System/Library/PrivateFrameworks/SkyLight.framework/Resources/WindowServer 10:00.00
 900 /Applications/Google Chrome.app/Contents/MacOS/Google Chrome 0:01.00
 901 /System/Library/Frameworks/VideoToolbox.framework/Versions/A/XPCServices/VTDecoderXPCService.xpc/Contents/MacOS/VTDecoderXPCService 0:00.50
 902 VTDecoderXPCService 0:01.50
garbage
`
	procs := parsePS(out)
	if len(procs) != 5 {
		t.Fatalf("procs = %+v", procs)
	}
	if p := procs[1]; p.pid != 357 || p.name != "WindowServer" || p.cpu != 600 {
		t.Errorf("WindowServer = %+v", p)
	}
	if p := procs[2]; p.name != "Google Chrome" || p.cpu != 1 {
		t.Errorf("chrome = %+v", p)
	}
}

func TestProcTrackerDelta(t *testing.T) {
	tr := newProcTracker([]string{"VTDecoderXPCService", "WindowServer"})
	now := t0
	first := tr.sample([]proc{{1, "VTDecoderXPCService", 10}, {2, "WindowServer", 100}, {3, "Other", 5}}, now)
	if first["VTDecoderXPCService"] != 0 || first["WindowServer"] != 0 || len(first) != 2 {
		t.Errorf("first = %v", first)
	}
	// 2s later: decoder pid 1 used 0.5s, new decoder pid 4 appeared (ignored
	// until next sample), WindowServer used 1s.
	second := tr.sample([]proc{{1, "VTDecoderXPCService", 10.5}, {4, "VTDecoderXPCService", 3}, {2, "WindowServer", 101}}, now.Add(2*time.Second))
	if second["VTDecoderXPCService"] != 25 || second["WindowServer"] != 50 {
		t.Errorf("second = %v", second)
	}
	third := tr.sample([]proc{{1, "VTDecoderXPCService", 11}, {4, "VTDecoderXPCService", 3.5}}, now.Add(3*time.Second))
	if third["VTDecoderXPCService"] != 100 || third["WindowServer"] != 0 {
		t.Errorf("third = %v", third)
	}
}

func TestProcessCPUSelf(t *testing.T) {
	sample := ProcessCPU("self")
	burn := time.Now().Add(50 * time.Millisecond)
	x := 0
	for time.Now().Before(burn) {
		x++
	}
	_ = x
	m := sample()
	if runtime.GOOS == "windows" {
		return
	}
	v, ok := m["self"]
	if !ok {
		t.Fatalf("no self in %v", m)
	}
	if v < 0 {
		t.Errorf("self = %v", v)
	}
	if v == 0 {
		t.Logf("self CPU reported 0 after busy loop (coarse rusage?)")
	}
}

func TestProcessCPUNamed(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("ps sampling is darwin-only")
	}
	sample := ProcessCPU("self", "WindowServer")
	first := sample()
	if v, ok := first["WindowServer"]; !ok || v != 0 {
		t.Errorf("first WindowServer = %v (%v)", v, ok)
	}
	time.Sleep(100 * time.Millisecond)
	second := sample()
	if v := second["WindowServer"]; v < 0 {
		t.Errorf("WindowServer = %v", v)
	}
}

// syncBuffer is a bytes.Buffer safe for the recorder goroutine and the test.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestLast(t *testing.T) {
	t0 := time.Unix(1000, 0)
	r := newRecorderAt(&bytes.Buffer{}, "viewer", nil, t0)
	if got := r.Last(); got.Frames != 0 || !got.Time.IsZero() {
		t.Fatalf("Last before Flush = %+v, want zero", got)
	}
	r.Frame(FrameTimes{Seq: 1})
	r.Frame(FrameTimes{Seq: 2})
	s, err := r.Flush(t0.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Last(); got.Frames != 2 || got.FPS != s.FPS || !got.Time.Equal(s.Time) {
		t.Fatalf("Last = %+v, want %+v", got, s)
	}
}

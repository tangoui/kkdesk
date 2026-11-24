// Package stats records per-frame timestamps on either end of a session,
// aggregates them once per second into JSON Lines (fps, bitrate, loss,
// latency percentiles, per-segment medians, CPU) and summarises such a log.
//
// Both ends of a loopback session share one clock, so latencies are plain
// differences of time.Time values; no clock synchronisation is attempted.
package stats

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"slices"
	"sync"
	"time"
)

// FrameTimes holds the timestamps of one video frame. Each side fills only
// the fields it observes and leaves the others zero: the host records
// Capture/Encoded/Sent, the viewer records Received/Decoded/Enqueued/Presented
// plus Capture copied from the sender's capture timestamp.
type FrameTimes struct {
	Seq                                                            uint64
	Capture, Encoded, Sent, Received, Decoded, Enqueued, Presented time.Time
}

// Segment names. A segment is measured between two consecutive non-zero
// timestamps of a frame, in pipeline order, so a viewer frame (which has no
// Encoded/Sent) yields SegCaptureReceived instead of the three host-side and
// network segments.
const (
	SegCaptureEncoded    = "capture_encoded"
	SegEncodedSent       = "encoded_sent"
	SegSentReceived      = "sent_received"
	SegReceivedDecoded   = "received_decoded"
	SegDecodedEnqueued   = "decoded_enqueued"
	SegEnqueuedPresented = "enqueued_presented"
	SegCaptureReceived   = "capture_received"
	// Host-side extra samples (Recorder.Sample), not pipeline segments:
	// SegCaptureDisplay is SCK display time − CaptureAt (how long after the
	// capture callback the frame is shown locally) and SegDisplayEncoded is
	// encoder output − display time.
	SegCaptureDisplay = "capture_display"
	SegDisplayEncoded = "display_encoded"
)

var stageNames = [...]string{"capture", "encoded", "sent", "received", "decoded", "enqueued", "presented"}

func (f FrameTimes) stages() [7]time.Time {
	return [7]time.Time{f.Capture, f.Encoded, f.Sent, f.Received, f.Decoded, f.Enqueued, f.Presented}
}

// latency is Presented−Capture when both are known (viewer), else
// Sent−Capture (host).
func (f FrameTimes) latency() (time.Duration, bool) {
	if f.Capture.IsZero() {
		return 0, false
	}
	if !f.Presented.IsZero() {
		return f.Presented.Sub(f.Capture), true
	}
	if !f.Sent.IsZero() {
		return f.Sent.Sub(f.Capture), true
	}
	return 0, false
}

// Second is one per-second aggregate, as written to the log.
type Second struct {
	Time       time.Time
	Role       string
	Frames     int
	FPS        float64
	BitrateBps float64
	LossPct    float64
	LatencyP50 time.Duration
	LatencyP95 time.Duration
	Segments   map[string]time.Duration // median of each segment
	CPU        map[string]float64       // percent of one core
}

// Report is the aggregate over a whole run.
type Report struct {
	Role           string
	Seconds        int // number of per-second lines
	Frames         int
	LatencyP50     time.Duration // exact, over all per-frame samples
	LatencyP95     time.Duration
	MeanFPS        float64 // mean of the per-second values
	MeanBitrateBps float64
	MeanLossPct    float64
	Segments       map[string]time.Duration // median over all per-frame samples
	CPU            map[string]float64       // mean percent per process
}

// line is the JSON form of one second. lat_ms and seg_samples_ms carry the
// raw per-frame samples (microsecond precision) so Summary can compute exact
// overall percentiles.
type line struct {
	Time         time.Time            `json:"time"`
	Role         string               `json:"role"`
	Frames       int                  `json:"frames"`
	FPS          float64              `json:"fps"`
	BitrateBps   float64              `json:"bitrate_bps"`
	LossPct      float64              `json:"loss_pct"`
	LatencyP50Ms float64              `json:"latency_p50_ms"`
	LatencyP95Ms float64              `json:"latency_p95_ms"`
	SegmentsMs   map[string]float64   `json:"segments_ms"`
	CPU          map[string]float64   `json:"cpu,omitempty"`
	LatMs        []float64            `json:"lat_ms"`
	SegSamplesMs map[string][]float64 `json:"seg_samples_ms"`
}

// DefaultMaxSamples caps the per-frame samples a Recorder keeps in memory
// for Report (per latency and per segment).
const DefaultMaxSamples = 1_000_000

// Recorder collects frames and writes one JSON line per Flush. It is safe
// for concurrent use.
type Recorder struct {
	w    io.Writer
	role string
	cpu  func() map[string]float64

	maxSamples int

	flushMu sync.Mutex // serialises Flush (cpu sampler is stateful)

	mu      sync.Mutex
	last    time.Time
	frames  int
	bytes   int64
	loss    float64
	lat     []time.Duration
	seg     map[string][]time.Duration
	allLat  []time.Duration
	allSeg  map[string][]time.Duration
	total   totals
	stop    chan struct{}
	done    chan struct{}
	started bool
	closed  bool
	lastSec Second // latest Flush result, for live display
}

type totals struct {
	seconds, frames    int
	fps, bitrate, loss float64
	cpuSum             map[string]float64
	cpuN               map[string]int
}

// NewRecorder returns a Recorder writing JSON Lines to w. role is written
// into every line ("host" or "viewer"). cpu, if non-nil, is called on every
// Flush (see ProcessCPU). Call Start to flush once per second.
func NewRecorder(w io.Writer, role string, cpu func() map[string]float64) *Recorder {
	return newRecorderAt(w, role, cpu, time.Now())
}

func newRecorderAt(w io.Writer, role string, cpu func() map[string]float64, now time.Time) *Recorder {
	return &Recorder{
		w:          w,
		role:       role,
		cpu:        cpu,
		maxSamples: DefaultMaxSamples,
		last:       now,
		seg:        map[string][]time.Duration{},
		allSeg:     map[string][]time.Duration{},
		total:      totals{cpuSum: map[string]float64{}, cpuN: map[string]int{}},
	}
}

// Frame records one frame.
func (r *Recorder) Frame(f FrameTimes) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.frames++
	if d, ok := f.latency(); ok {
		r.lat = append(r.lat, d)
		if len(r.allLat) < r.maxSamples {
			r.allLat = append(r.allLat, d)
		}
	}
	st := f.stages()
	prev := -1
	for i, t := range st {
		if t.IsZero() {
			continue
		}
		if prev >= 0 {
			k := stageNames[prev] + "_" + stageNames[i]
			d := t.Sub(st[prev])
			r.seg[k] = append(r.seg[k], d)
			if len(r.allSeg[k]) < r.maxSamples {
				r.allSeg[k] = append(r.allSeg[k], d)
			}
		}
		prev = i
	}
}

// Sample records one extra named duration, aggregated like a segment
// (per-second and overall median) but outside the frame pipeline.
func (r *Recorder) Sample(name string, d time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seg[name] = append(r.seg[name], d)
	if len(r.allSeg[name]) < r.maxSamples {
		r.allSeg[name] = append(r.allSeg[name], d)
	}
}

// AddBytes adds n bytes of video payload sent or received.
func (r *Recorder) AddBytes(n int) {
	r.mu.Lock()
	r.bytes += int64(n)
	r.mu.Unlock()
}

// Loss sets the current packet loss percentage; it is reported in every
// following line until changed.
func (r *Recorder) Loss(pct float64) {
	r.mu.Lock()
	r.loss = pct
	r.mu.Unlock()
}

// Flush aggregates everything recorded since the previous Flush (or since
// NewRecorder), writes one JSON line and returns the aggregate. Rates are
// divided by the actual elapsed time up to now.
func (r *Recorder) Flush(now time.Time) (Second, error) {
	r.flushMu.Lock()
	defer r.flushMu.Unlock()

	var cpu map[string]float64
	if r.cpu != nil {
		cpu = r.cpu()
	}

	r.mu.Lock()
	elapsed := now.Sub(r.last).Seconds()
	s := Second{
		Time:     now,
		Role:     r.role,
		Frames:   r.frames,
		LossPct:  r.loss,
		Segments: map[string]time.Duration{},
		CPU:      cpu,
	}
	if elapsed > 0 {
		s.FPS = float64(r.frames) / elapsed
		s.BitrateBps = float64(r.bytes) * 8 / elapsed
	}
	lat, seg := r.lat, r.seg
	r.last, r.frames, r.bytes, r.lat, r.seg = now, 0, 0, nil, map[string][]time.Duration{}

	t := &r.total
	t.seconds++
	t.frames += s.Frames
	t.fps += s.FPS
	t.bitrate += s.BitrateBps
	t.loss += s.LossPct
	for k, v := range cpu {
		t.cpuSum[k] += v
		t.cpuN[k]++
	}
	r.mu.Unlock()

	slices.Sort(lat)
	s.LatencyP50 = percentile(lat, 50)
	s.LatencyP95 = percentile(lat, 95)
	l := line{
		Time:         now,
		Role:         s.Role,
		Frames:       s.Frames,
		FPS:          s.FPS,
		BitrateBps:   s.BitrateBps,
		LossPct:      s.LossPct,
		LatencyP50Ms: toMs(s.LatencyP50),
		LatencyP95Ms: toMs(s.LatencyP95),
		SegmentsMs:   map[string]float64{},
		CPU:          cpu,
		LatMs:        msSlice(lat),
		SegSamplesMs: map[string][]float64{},
	}
	for k, v := range seg {
		l.SegSamplesMs[k] = msSlice(v)
		slices.Sort(v)
		s.Segments[k] = percentile(v, 50)
		l.SegmentsMs[k] = toMs(s.Segments[k])
	}

	r.mu.Lock()
	r.lastSec = s
	r.mu.Unlock()

	b, err := json.Marshal(l)
	if err != nil {
		return s, err
	}
	b = append(b, '\n')
	_, err = r.w.Write(b)
	return s, err
}

// Report aggregates everything flushed so far from memory (per-frame samples
// are capped at DefaultMaxSamples). Frames not yet flushed are not included.
func (r *Recorder) Report() Report {
	r.mu.Lock()
	defer r.mu.Unlock()
	t := r.total
	rep := Report{
		Role:     r.role,
		Seconds:  t.seconds,
		Frames:   t.frames,
		Segments: map[string]time.Duration{},
		CPU:      map[string]float64{},
	}
	if t.seconds > 0 {
		n := float64(t.seconds)
		rep.MeanFPS, rep.MeanBitrateBps, rep.MeanLossPct = t.fps/n, t.bitrate/n, t.loss/n
	}
	lat := slices.Clone(r.allLat)
	slices.Sort(lat)
	rep.LatencyP50, rep.LatencyP95 = percentile(lat, 50), percentile(lat, 95)
	for k, v := range r.allSeg {
		v = slices.Clone(v)
		slices.Sort(v)
		rep.Segments[k] = percentile(v, 50)
	}
	for k, sum := range t.cpuSum {
		rep.CPU[k] = sum / float64(t.cpuN[k])
	}
	return rep
}

// Last returns the aggregate of the most recent Flush (zero before the
// first), for live display such as a session window's fps and latency.
func (r *Recorder) Last() Second {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lastSec
}

// Start flushes once per second in a goroutine until Close.
func (r *Recorder) Start() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.started || r.closed {
		return
	}
	r.started = true
	r.stop, r.done = make(chan struct{}), make(chan struct{})
	go func() {
		defer close(r.done)
		tk := time.NewTicker(time.Second)
		defer tk.Stop()
		for {
			select {
			case <-r.stop:
				return
			case now := <-tk.C:
				r.Flush(now) // write errors are not fatal for measurement
			}
		}
	}()
}

// Close stops the ticker started by Start and flushes frames or bytes
// recorded since the last Flush. It does not close the writer. Calling it
// more than once is harmless.
func (r *Recorder) Close() error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil
	}
	r.closed = true
	started := r.started
	r.mu.Unlock()
	if started {
		close(r.stop)
		<-r.done
	}
	r.mu.Lock()
	pending := r.frames > 0 || r.bytes > 0
	r.mu.Unlock()
	if pending {
		_, err := r.Flush(time.Now())
		return err
	}
	return nil
}

// Summary reads a JSON Lines log written by a Recorder and aggregates it.
// Latency percentiles and segment medians are exact over all per-frame
// samples; fps, bitrate, loss and CPU are means of the per-second values.
func Summary(path string) (Report, error) {
	f, err := os.Open(path)
	if err != nil {
		return Report{}, err
	}
	defer f.Close()

	rep := Report{Segments: map[string]time.Duration{}, CPU: map[string]float64{}}
	var lat []time.Duration
	seg := map[string][]time.Duration{}
	cpuN := map[string]int{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 64*1024*1024)
	n := 0
	for sc.Scan() {
		n++
		b := sc.Bytes()
		if len(b) == 0 {
			continue
		}
		var l line
		if err := json.Unmarshal(b, &l); err != nil {
			return Report{}, fmt.Errorf("%s:%d: %w", path, n, err)
		}
		if rep.Role == "" {
			rep.Role = l.Role
		}
		rep.Seconds++
		rep.Frames += l.Frames
		rep.MeanFPS += l.FPS
		rep.MeanBitrateBps += l.BitrateBps
		rep.MeanLossPct += l.LossPct
		for _, v := range l.LatMs {
			lat = append(lat, fromMs(v))
		}
		for k, vs := range l.SegSamplesMs {
			for _, v := range vs {
				seg[k] = append(seg[k], fromMs(v))
			}
		}
		for k, v := range l.CPU {
			rep.CPU[k] += v
			cpuN[k]++
		}
	}
	if err := sc.Err(); err != nil {
		return Report{}, err
	}
	if rep.Seconds > 0 {
		s := float64(rep.Seconds)
		rep.MeanFPS /= s
		rep.MeanBitrateBps /= s
		rep.MeanLossPct /= s
	}
	for k := range rep.CPU {
		rep.CPU[k] /= float64(cpuN[k])
	}
	slices.Sort(lat)
	rep.LatencyP50, rep.LatencyP95 = percentile(lat, 50), percentile(lat, 95)
	for k, v := range seg {
		slices.Sort(v)
		rep.Segments[k] = percentile(v, 50)
	}
	return rep, nil
}

// percentile returns the nearest-rank p-th percentile of sorted s (the
// median of an even-sized set is the lower middle value); 0 if s is empty.
func percentile(s []time.Duration, p float64) time.Duration {
	if len(s) == 0 {
		return 0
	}
	i := int(math.Ceil(p/100*float64(len(s)))) - 1
	return s[max(0, min(i, len(s)-1))]
}

// toMs converts to milliseconds with microsecond precision.
func toMs(d time.Duration) float64 {
	return math.Round(float64(d)/float64(time.Microsecond)) / 1000
}

func fromMs(v float64) time.Duration {
	return time.Duration(math.Round(v * float64(time.Millisecond)))
}

func msSlice(s []time.Duration) []float64 {
	out := make([]float64, len(s))
	for i, d := range s {
		out[i] = toMs(d)
	}
	return out
}

package stats

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// remoteFrames shifts viewerFrames' Capture onto a host clock that runs
// hostAhead ahead of the viewer, as the viewer receives it over the wire.
func remoteFrames(n, base int, start time.Time, hostAhead, rtt time.Duration, synced bool) []FrameTimes {
	fs := viewerFrames(n, base, start)
	for i := range fs {
		fs[i].Capture = fs[i].Capture.Add(hostAhead)
		fs[i].Clock = Clock{Remote: true, Synced: synced, Offset: hostAhead, RTT: rtt}
	}
	return fs
}

func TestRemoteClockConversion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log.jsonl")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	r := newRecorderAt(f, "viewer", nil, t0)
	// Second 1: 20 frames before the clock is synced, then 60 synced frames
	// with latencies 10..69 ms on a host clock 2 s ahead.
	for _, fr := range remoteFrames(20, 500, t0, 2*time.Second, 0, false) {
		r.Frame(fr)
	}
	all := remoteFrames(100, 10, t0, 2*time.Second, 4*time.Millisecond, true)
	for _, fr := range all[:60] {
		r.Frame(fr)
	}
	sec, err := r.Flush(t0.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if sec.Frames != 80 || sec.ClockUnsynced != 20 {
		t.Errorf("frames/unsynced = %d/%d, want 80/20", sec.Frames, sec.ClockUnsynced)
	}
	if sec.LatencyP50 != ms(39) || sec.LatencyP95 != ms(66) {
		t.Errorf("p50/p95 = %v/%v, want 39ms/66ms", sec.LatencyP50, sec.LatencyP95)
	}
	if sec.Segments[SegCaptureReceived] != ms(33) || sec.Segments[SegReceivedDecoded] != ms(2) {
		t.Errorf("segments = %v", sec.Segments)
	}
	if sec.Clock == nil || !sec.Clock.Synced || sec.Clock.Offset != 2*time.Second || sec.Clock.RTT != ms(4) {
		t.Errorf("clock = %+v", sec.Clock)
	}

	// Second 2: only unsynced frames (e.g. the clock estimate was lost).
	for _, fr := range remoteFrames(10, 10, t0.Add(time.Second), 2*time.Second, 0, false) {
		r.Frame(fr)
	}
	sec, err = r.Flush(t0.Add(2 * time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if sec.LatencyP50 != 0 || sec.Segments[SegCaptureReceived] != 0 || sec.Segments[SegReceivedDecoded] != ms(2) {
		t.Errorf("unsynced second: p50 %v, segments %v", sec.LatencyP50, sec.Segments)
	}
	if sec.Clock == nil || sec.Clock.Synced || sec.ClockUnsynced != 10 {
		t.Errorf("unsynced second: clock %+v unsynced %d", sec.Clock, sec.ClockUnsynced)
	}

	// Second 3: the rest of the synced frames.
	for _, fr := range all[60:] {
		r.Frame(fr)
	}
	if _, err := r.Flush(t0.Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	var l1, l2 map[string]any
	json.Unmarshal([]byte(lines[0]), &l1)
	json.Unmarshal([]byte(lines[1]), &l2)
	if l1["clock_synced"] != true || l1["clock_offset_ms"] != 2000.0 || l1["clock_rtt_ms"] != 4.0 || l1["clock_unsynced_frames"] != 20.0 {
		t.Errorf("line 1 clock fields: %s", lines[0])
	}
	if l2["clock_synced"] != false || l2["clock_offset_ms"] != nil {
		t.Errorf("line 2 clock fields: %s", lines[1])
	}

	rep, err := Summary(path)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Frames != 130 || rep.LatencyP50 != ms(59) || rep.LatencyP95 != ms(104) {
		t.Errorf("summary frames %d p50/p95 %v/%v, want 130 59ms/104ms", rep.Frames, rep.LatencyP50, rep.LatencyP95)
	}
	if rep.ClockSeconds != 3 || rep.ClockSyncedSeconds != 2 || rep.ClockUnsyncedFrames != 30 {
		t.Errorf("summary clock seconds/synced/unsynced = %d/%d/%d", rep.ClockSeconds, rep.ClockSyncedSeconds, rep.ClockUnsyncedFrames)
	}
	if rep.ClockOffset != 2*time.Second || rep.ClockRTT != ms(4) {
		t.Errorf("summary offset/rtt = %v/%v", rep.ClockOffset, rep.ClockRTT)
	}
	mem := r.Report()
	if mem.LatencyP50 != rep.LatencyP50 || mem.ClockSeconds != 3 || mem.ClockSyncedSeconds != 2 ||
		mem.ClockUnsyncedFrames != 30 || mem.ClockOffset != rep.ClockOffset || mem.ClockRTT != rep.ClockRTT {
		t.Errorf("memory report %+v != file report %+v", mem, rep)
	}
}

func TestLocalClockLinesHaveNoClockFields(t *testing.T) {
	var buf bytes.Buffer
	r := newRecorderAt(&buf, "host", nil, t0)
	r.Frame(FrameTimes{Capture: t0, Encoded: t0.Add(ms(1)), Sent: t0.Add(ms(2))})
	sec, err := r.Flush(t0.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if sec.Clock != nil || strings.Contains(buf.String(), "clock_") {
		t.Errorf("host line has clock fields: %s", buf.String())
	}
	if rep := r.Report(); rep.ClockSeconds != 0 {
		t.Errorf("host report clock seconds = %d", rep.ClockSeconds)
	}
}

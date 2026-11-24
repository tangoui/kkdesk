package app

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"mmdesk/internal/capture"
	"mmdesk/internal/stats"
)

func TestAnnexBTypes(t *testing.T) {
	b := []byte{0, 0, 0, 1, 0x67, 1, 2, 0, 0, 0, 1, 0x68, 3, 0, 0, 1, 0x65, 0, 0, 3, 1}
	if got := annexBTypes(b); !reflect.DeepEqual(got, []int{7, 8, 5}) {
		t.Fatalf("annexBTypes = %v", got)
	}
}

func TestPercentiles(t *testing.T) {
	var d []time.Duration
	for i := 100; i >= 1; i-- {
		d = append(d, time.Duration(i)*time.Millisecond)
	}
	m, p95 := percentiles(d)
	if m != 51*time.Millisecond || p95 != 96*time.Millisecond {
		t.Fatalf("percentiles = %v %v", m, p95)
	}
	if m, p := percentiles([]time.Duration{7}); m != 7 || p != 7 {
		t.Fatalf("single = %v %v", m, p)
	}
}

func TestStatsSummary(t *testing.T) {
	s := statsSummary(stats.Second{FPS: 59.5, BitrateBps: 8e6, Frames: 60, LatencyP50: 5 * time.Millisecond,
		LatencyP95: 9 * time.Millisecond, Segments: map[string]time.Duration{"capture_encoded": 4 * time.Millisecond},
		CPU: map[string]float64{"self": 12}},
		capture.Stats{Complete: 10, Resent: 2, AcquireTotal: 10 * time.Millisecond})
	for _, want := range []string{"59.5 fps", "8.00 Mbps", "p50 5.0ms", "capture_encoded 4.0ms", "cpu self 12%", "resent 2", "acquire avg 1.0ms"} {
		if !strings.Contains(s, want) {
			t.Errorf("summary %q lacks %q", s, want)
		}
	}
}

func TestSystemInfo(t *testing.T) {
	info := SystemInfo()
	if len(info) < 5 || !strings.HasPrefix(info[0], "mmdesk version: ") {
		t.Fatalf("SystemInfo = %q", info)
	}
	t.Logf("%s", strings.Join(info, "\n"))
}

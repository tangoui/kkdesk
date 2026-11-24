package stats

import (
	"errors"
	"strconv"
	"strings"
	"time"
)

// selfTracker turns cumulative CPU time of this process into percent of one
// core between samples.
type selfTracker struct {
	prevCPU time.Duration
	prevAt  time.Time
	ok      bool
}

func newSelfTracker(now time.Time) *selfTracker {
	cpu, ok := selfCPU()
	return &selfTracker{prevCPU: cpu, prevAt: now, ok: ok}
}

func (t *selfTracker) sample(now time.Time) (float64, bool) {
	if !t.ok {
		return 0, false
	}
	cpu, ok := selfCPU()
	if !ok {
		return 0, false
	}
	wall := now.Sub(t.prevAt)
	d := cpu - t.prevCPU
	t.prevCPU, t.prevAt = cpu, now
	if wall <= 0 || d < 0 {
		return 0, true
	}
	return float64(d) / float64(wall) * 100, true
}

// psArgs lists every process with pid, cumulative CPU time and executable
// path. comm must be the last column: macOS ps truncates non-final comm to
// 16 bytes ("/System/Library/"), which made every named lookup miss.
var psArgs = []string{"-A", "-o", "pid=,time=,comm="}

// proc is one line of `ps` run with psArgs.
type proc struct {
	pid  int
	name string  // basename of comm
	cpu  float64 // cumulative CPU seconds
}

// parsePS parses `ps -A -o pid=,time=,comm=` output. comm may contain
// spaces, so it is everything after the second field. Unparseable lines
// are skipped.
func parsePS(out string) []proc {
	var ps []proc
	for _, ln := range strings.Split(out, "\n") {
		f := strings.Fields(ln)
		if len(f) < 3 {
			continue
		}
		pid, err := strconv.Atoi(f[0])
		if err != nil {
			continue
		}
		cpu, err := parseCPUTime(f[1])
		if err != nil {
			continue
		}
		// Rejoin with single spaces; ps pads columns, never comm itself.
		comm := strings.Join(f[2:], " ")
		if i := strings.LastIndexByte(comm, '/'); i >= 0 && i < len(comm)-1 {
			comm = comm[i+1:]
		}
		ps = append(ps, proc{pid: pid, name: comm, cpu: cpu})
	}
	return ps
}

// parseCPUTime parses ps cumulative CPU time: "[[dd-]hh:]mm:ss[.cc]"
// (macOS prints "mmm:ss.cc", procps prints "[dd-]hh:mm:ss").
func parseCPUTime(s string) (float64, error) {
	bad := errors.New("stats: bad cpu time " + strconv.Quote(s))
	var days float64
	if d, rest, ok := strings.Cut(s, "-"); ok {
		n, err := strconv.Atoi(d)
		if err != nil || n < 0 {
			return 0, bad
		}
		days, s = float64(n), rest
	}
	parts := strings.Split(s, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, bad
	}
	sec, err := strconv.ParseFloat(parts[len(parts)-1], 64)
	if err != nil || sec < 0 {
		return 0, bad
	}
	total := sec
	mul := 60.0
	for i := len(parts) - 2; i >= 0; i-- {
		n, err := strconv.Atoi(parts[i])
		if err != nil || n < 0 {
			return 0, bad
		}
		total += float64(n) * mul
		mul *= 60
	}
	return total + days*86400, nil
}

// procTracker reports CPU percent per process name from successive ps
// snapshots, summing over all pids with that name. A pid only contributes
// once it has been seen in two consecutive snapshots.
type procTracker struct {
	names  []string
	want   map[string]bool
	prev   map[int]proc
	prevAt time.Time
}

func newProcTracker(names []string) *procTracker {
	t := &procTracker{want: map[string]bool{}}
	for _, n := range names {
		if n == "self" || t.want[n] {
			continue
		}
		t.want[n] = true
		t.names = append(t.names, n)
	}
	return t
}

func (t *procTracker) sample(ps []proc, now time.Time) map[string]float64 {
	out := make(map[string]float64, len(t.names))
	for _, n := range t.names {
		out[n] = 0
	}
	cur := map[int]proc{}
	for _, p := range ps {
		if t.want[p.name] {
			cur[p.pid] = p
		}
	}
	if t.prev != nil {
		if wall := now.Sub(t.prevAt).Seconds(); wall > 0 {
			for pid, p := range cur {
				old, ok := t.prev[pid]
				if !ok || old.name != p.name || p.cpu < old.cpu {
					continue
				}
				out[p.name] += (p.cpu - old.cpu) / wall * 100
			}
		}
	}
	t.prev, t.prevAt = cur, now
	return out
}

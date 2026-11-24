package stats

import (
	"os/exec"
	"sync"
	"time"
)

// ProcessCPU returns a sampler reporting CPU usage in percent of one core
// since its previous call (or since ProcessCPU for the first call): "self"
// for this process (getrusage), plus one entry per named process (for
// example "VTDecoderXPCService", "VTEncoderXPCService", "WindowServer"),
// summed over all pids whose executable basename matches, taken from
// cumulative CPU time in `ps`. Named processes report 0 on the first call.
// The name "self" is reserved for this process.
func ProcessCPU(names ...string) func() map[string]float64 {
	var mu sync.Mutex
	self := newSelfTracker(time.Now())
	tr := newProcTracker(names)
	return func() map[string]float64 {
		mu.Lock()
		defer mu.Unlock()
		var ps []proc
		if len(tr.names) > 0 {
			if out, err := exec.Command("ps", psArgs...).Output(); err == nil {
				ps = parsePS(string(out))
			}
		}
		now := time.Now()
		m := tr.sample(ps, now)
		if v, ok := self.sample(now); ok {
			m["self"] = v
		}
		return m
	}
}

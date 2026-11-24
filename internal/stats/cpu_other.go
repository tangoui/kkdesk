//go:build !darwin

package stats

import (
	"sync"
	"time"
)

// ProcessCPU returns a sampler reporting this process's CPU usage as "self"
// (percent of one core since the previous call) where getrusage is
// available, and an empty map elsewhere (Windows). Other process names are
// only sampled on macOS and are ignored here.
func ProcessCPU(names ...string) func() map[string]float64 {
	var mu sync.Mutex
	self := newSelfTracker(time.Now())
	return func() map[string]float64 {
		mu.Lock()
		defer mu.Unlock()
		m := map[string]float64{}
		if v, ok := self.sample(time.Now()); ok {
			m["self"] = v
		}
		return m
	}
}

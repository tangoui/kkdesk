//go:build unix

package stats

import (
	"syscall"
	"time"
)

// selfCPU returns the user+system CPU time consumed by this process.
func selfCPU() (time.Duration, bool) {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		return 0, false
	}
	return time.Duration(ru.Utime.Nano() + ru.Stime.Nano()), true
}

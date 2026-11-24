//go:build !unix

package stats

import "time"

// selfCPU is not implemented on this platform.
func selfCPU() (time.Duration, bool) { return 0, false }

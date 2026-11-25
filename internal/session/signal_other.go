//go:build !windows

package session

// closedEarlyOS: syscall.ECONNRESET in closedEarly covers these systems.
func closedEarlyOS(error) bool { return false }

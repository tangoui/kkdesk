//go:build !windows || !cgo

package privacy

// SessionLocked reports false: only Windows keeps a normal process off the
// lock screen.
func SessionLocked() (bool, error) { return false, nil }

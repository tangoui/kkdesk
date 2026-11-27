//go:build (!windows && !darwin) || !cgo

package privacy

// SessionLocked reports false on unsupported systems.
func SessionLocked() (bool, error) { return false, nil }

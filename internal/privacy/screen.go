//go:build (!darwin && !windows) || !cgo

package privacy

func NewScreen() Screen { return unsupportedScreen{} }

func NewLocker() Locker {
	return confirmedLocker{request: func() error { return errLockUnsupported }}
}

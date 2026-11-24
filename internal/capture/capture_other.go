//go:build !darwin || !cgo

package capture

// Start is not implemented on this platform yet.
func Start(opts Options, out func(Frame)) (Session, error) { return nil, ErrUnsupported }

// HasPermission always reports false on unsupported platforms.
func HasPermission() bool { return false }

// RequestPermission does nothing on unsupported platforms.
func RequestPermission() {}

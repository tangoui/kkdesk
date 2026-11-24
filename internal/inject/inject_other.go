//go:build !windows && (!darwin || !cgo)

package inject

import "github.com/tangoui/kkdesk/internal/input"

// HasPermission always reports false on unsupported platforms.
func HasPermission() bool { return false }

// RequestPermission does nothing on unsupported platforms.
func RequestPermission() {}

// Injector is not implemented on this platform.
type Injector struct{}

// New returns ErrUnsupported on this platform.
func New() (*Injector, error) { return nil, ErrUnsupported }

// Refresh does nothing on this platform.
func (in *Injector) Refresh() {}

// Bounds returns the zero Rect on this platform.
func (in *Injector) Bounds() Rect { return Rect{} }

// Apply returns ErrUnsupported on this platform.
func (in *Injector) Apply(e input.Event) error { return ErrUnsupported }

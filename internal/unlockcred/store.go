// Package unlockcred stores controller-supplied unlock credentials locally.
package unlockcred

import "errors"

var (
	ErrNotFound         = errors.New("unlock credential not found")
	errStoreUnavailable = errors.New("system credential store unavailable")
)

type Store interface {
	Read(account string) ([]byte, error)
	Write(account string, secret []byte) error
	Delete(account string) error
}

// System returns the native credential store, or nil on unsupported platforms.
func System() Store { return nativeStore("kkdesk.unlock") }

//go:build windows

package session

import (
	"errors"
	"syscall"
)

// closedEarlyOS reports the Winsock errors of a client that closed early:
// a reset (WSAECONNRESET) or, when the host's nonce reached a socket the
// client had already closed, an abort (WSAECONNABORTED). Go's
// syscall.ECONNRESET is not WSAECONNRESET on Windows.
func closedEarlyOS(err error) bool {
	return errors.Is(err, syscall.WSAECONNRESET) || errors.Is(err, syscall.WSAECONNABORTED)
}

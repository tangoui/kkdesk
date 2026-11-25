package files

import "os"

// ListLocal lists a directory of this machine the way the server lists the
// host's (directories first, then by name, at most MaxList entries), for the
// local pane of a file browser.
func ListLocal(path string) (Listing, error) {
	l, err := list(path)
	if err != nil {
		return Listing{}, err
	}
	return *l, nil
}

// LocalHome describes this machine like Client.Home describes the host.
func LocalHome() Home { return *home() }

// MkdirLocal creates one directory on this machine; ErrExists if it exists.
func MkdirLocal(path string) error {
	p, err := absPath(path)
	if err != nil {
		return err
	}
	return localError(os.Mkdir(p, 0o755))
}

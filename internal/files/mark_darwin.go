package files

import (
	"fmt"
	"time"

	"golang.org/x/sys/unix"
)

// markFromInternet sets the quarantine attribute on a downloaded file, so
// Gatekeeper checks it before it is opened or run, like a browser
// download. Best effort.
func markFromInternet(path string) {
	v := fmt.Sprintf("0081;%08x;kkdesk;", time.Now().Unix())
	_ = unix.Setxattr(path, "com.apple.quarantine", []byte(v), 0)
}

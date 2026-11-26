package files

import (
	"os"
	"time"
)

// markFromInternet tags a downloaded file with the Internet zone
// (Zone.Identifier stream), so Windows warns before running it like a
// browser download. Writing the stream updates the file's modification
// time, which is put back. Best effort.
func markFromInternet(path string) {
	fi, err := os.Stat(path)
	if err != nil {
		return
	}
	if os.WriteFile(path+":Zone.Identifier", []byte("[ZoneTransfer]\r\nZoneId=3\r\n"), 0o644) == nil {
		_ = os.Chtimes(path, time.Time{}, fi.ModTime())
	}
}

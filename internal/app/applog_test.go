package app

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A GUI-subsystem build has no usable stderr: the log file must still get
// every line.
func TestAppLogWithoutStderr(t *testing.T) {
	dead, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatal(err)
	}
	dead.Close() // writes to it fail, like an invalid handle
	saved := os.Stderr
	os.Stderr = dead
	defer func() { os.Stderr = saved }()

	path := filepath.Join(t.TempDir(), "kkdesk.log")
	l, err := OpenAppLog(path)
	if err != nil {
		t.Fatal(err)
	}
	log.Printf("first line")
	log.Printf("second line")
	l.Close()
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), "first line") || !strings.Contains(string(b), "second line") {
		t.Fatalf("log file = %q; want both lines although stderr is unusable", b)
	}
}

package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tangoui/kkdesk/internal/files"
)

// TestFilesOnlySession connects a files-only viewer to a host over
// loopback: no capture starts, the host reports the mode, and turning the
// 文件传输 permission off denies requests.
func TestFilesOnlySession(t *testing.T) {
	h := NewHost(HostConfig{Pairing: testHostIdentity(t), Addr: "127.0.0.1:0", Password: "123456", AutoAccept: true}, nil)
	if err := h.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	v := NewViewer(ViewerConfig{Addr: h.ListenAddr(), Password: "123456", FilesOnly: true}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := v.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	cl := v.Files()
	if cl == nil || v.Files() != cl {
		t.Fatal("Files() should return one client per session")
	}
	if _, err := cl.Home(ctx); err != nil {
		t.Fatal(err)
	}
	if st := h.State(); st.Phase != HostActive || !st.FilesOnly || !st.FileAccess {
		t.Fatalf("host state %+v, want active files-only with file access", st)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	l, err := cl.List(ctx, dir)
	if err != nil || len(l.Entries) != 1 || l.Entries[0].Name != "a.txt" {
		t.Fatalf("List = %+v, %v", l, err)
	}
	h.SetFileAccess(false)
	if st := h.State(); st.FileAccess {
		t.Fatal("FileAccess still on")
	}
	if _, err := cl.List(ctx, dir); !errors.Is(err, files.ErrDenied) {
		t.Fatalf("List with access off: %v, want ErrDenied", err)
	}
	h.SetFileAccess(true)
	if _, err := cl.List(ctx, dir); err != nil {
		t.Fatalf("List after access back on: %v", err)
	}
	v.Close()
	deadline := time.Now().Add(5 * time.Second)
	for h.State().Phase == HostActive && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if st := h.State(); st.Phase != HostListening {
		t.Fatalf("host after the viewer left: %+v", st)
	}
}

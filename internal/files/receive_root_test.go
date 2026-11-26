package files

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestUploadCleanupFollowsDestinationDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("renaming a directory containing an open file requires Unix semantics")
	}
	r := newRig(t)
	dir := t.TempDir()
	dst := filepath.Join(dir, "destination")
	if err := os.Mkdir(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	const id = 1
	if err := r.s.uploadBegin(&msg{Transfer: id, Dir: dst, Rel: "file.txt"}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.s.cancel(id) })
	moved := filepath.Join(dir, "renamed")
	if err := os.Rename(dst, moved); err != nil {
		t.Fatal(err)
	}
	r.s.cancel(id)
	noParts(t, moved)
}

func TestIncomingDoesNotCommitAfterParentMovesOut(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("renaming a directory containing an open file requires Unix semantics")
	}
	dir := t.TempDir()
	dst := filepath.Join(dir, "destination")
	parent := filepath.Join(dst, "nested")
	if err := os.MkdirAll(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	root, err := openReceiveRoot(dst)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	in, err := newIncoming(root, []string{"nested", "file.txt"}, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer in.abort()
	moved := filepath.Join(dir, "moved")
	if err := os.Rename(parent, moved); err != nil {
		t.Fatal(err)
	}
	if _, err := in.finish(fmt.Sprintf("%x", sha256.Sum256(nil)), nil); err == nil {
		t.Error("committed a file after its parent moved out of the receive root")
	}
	if entries, err := os.ReadDir(moved); err != nil || len(entries) != 0 {
		t.Fatalf("moved directory is not empty: %v, %v", entries, err)
	}
}

func TestIncomingKeepsFilesUnderReceiveRoot(t *testing.T) {
	dir := t.TempDir()
	dst, sibling := filepath.Join(dir, "destination"), filepath.Join(dir, "sibling")
	for _, path := range []string{dst, sibling, filepath.Join(dst, "inside")} {
		if err := os.Mkdir(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for link, target := range map[string]string{"local-link": "inside", "outside-link": "../sibling"} {
		if err := os.Symlink(target, filepath.Join(dst, link)); err != nil {
			if runtime.GOOS == "windows" {
				t.Skipf("creating symlinks is unavailable: %v", err)
			}
			t.Fatal(err)
		}
	}
	root, err := openReceiveRoot(dst)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	in, err := newIncoming(root, []string{"local-link", "file.txt"}, 0, 0, 0)
	if err != nil {
		t.Fatalf("link within destination: %v", err)
	}
	in.abort()
	noParts(t, dst)

	if in, err := newIncoming(root, []string{"outside-link", "file.txt"}, 0, 0, 0); err == nil {
		in.abort()
		t.Fatal("opened a destination outside the receive root")
	}
	if entries, err := os.ReadDir(sibling); err != nil || len(entries) != 0 {
		t.Fatalf("sibling directory changed: %v, %v", entries, err)
	}
}

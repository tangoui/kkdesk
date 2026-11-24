package files

import (
	"bytes"
	"context"
	"errors"
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type rig struct {
	c       *Client
	s       *Server
	ce, se  *PipeEnd // client end, server end
	allowed atomic.Bool
}

func newRig(t *testing.T) *rig {
	t.Helper()
	a, b := Pipe()
	r := &rig{ce: a, se: b}
	r.allowed.Store(true)
	r.s = NewServer(b, r.allowed.Load)
	r.c = NewClient(a)
	r.s.SetLogf(t.Logf)
	r.c.SetLogf(t.Logf)
	t.Cleanup(func() { a.Close() })
	return r
}

func testCtx(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func makeFile(t *testing.T, path string, size int, seed uint64) []byte {
	t.Helper()
	b := make([]byte, size)
	r := rand.New(rand.NewPCG(seed, seed*7+1))
	for i := range b {
		b[i] = byte(r.Uint32())
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return b
}

var oldTime = time.Date(2020, 5, 6, 7, 8, 9, 0, time.UTC)

// noParts fails if any temporary file is left under dir.
func noParts(t *testing.T, dir string) {
	t.Helper()
	filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && strings.HasSuffix(d.Name(), partSuffix) {
			t.Errorf("temporary file left behind: %s", p)
		}
		return nil
	})
}

// eventually polls f for up to 3 s.
func eventually(t *testing.T, what string, f func() bool) {
	t.Helper()
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if f() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

func countParts(dir string) int {
	n := 0
	filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && strings.HasSuffix(d.Name(), partSuffix) {
			n++
		}
		return nil
	})
	return n
}

func TestHome(t *testing.T) {
	r := newRig(t)
	h, err := r.c.Home(testCtx(t))
	if err != nil {
		t.Fatal(err)
	}
	want, _ := os.UserHomeDir()
	if h.Dir != want {
		t.Errorf("Home.Dir = %q, want %q", h.Dir, want)
	}
	if len(h.Volumes) == 0 {
		t.Error("no volumes")
	}
	if runtime.GOOS != "windows" && h.Volumes[0] != "/" {
		t.Errorf("volumes = %v", h.Volumes)
	}
	if h.Sep != string(os.PathSeparator) || h.OS != runtime.GOOS {
		t.Errorf("Sep %q OS %q", h.Sep, h.OS)
	}
}

func TestList(t *testing.T) {
	r := newRig(t)
	dir := t.TempDir()
	makeFile(t, filepath.Join(dir, "b.txt"), 10, 1)
	makeFile(t, filepath.Join(dir, "A.txt"), 3, 2)
	makeFile(t, filepath.Join(dir, ".hidden"), 1, 3)
	os.Mkdir(filepath.Join(dir, "zdir"), 0o755)
	os.Mkdir(filepath.Join(dir, "adir"), 0o755)
	os.Chtimes(filepath.Join(dir, "b.txt"), oldTime, oldTime)

	l, err := r.c.List(testCtx(t), dir+string(os.PathSeparator)+".")
	if err != nil {
		t.Fatal(err)
	}
	if l.Path != dir || l.Truncated {
		t.Errorf("Path %q Truncated %v", l.Path, l.Truncated)
	}
	var names []string
	for _, e := range l.Entries {
		names = append(names, e.Name)
	}
	if got, want := strings.Join(names, ","), "adir,zdir,.hidden,A.txt,b.txt"; got != want {
		t.Fatalf("order %s, want %s", got, want)
	}
	e := l.Entries[4]
	if e.Size != 10 || e.IsDir || !e.ModTime.Equal(oldTime) || e.Path != filepath.Join(dir, "b.txt") {
		t.Errorf("b.txt entry %+v", e)
	}
	if !l.Entries[2].Hidden || l.Entries[3].Hidden {
		t.Errorf("hidden flags %+v %+v", l.Entries[2], l.Entries[3])
	}
	if !l.Entries[0].IsDir {
		t.Errorf("adir not a dir")
	}
	if _, err := r.c.List(testCtx(t), filepath.Join(dir, "missing")); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing dir: %v", err)
	}
	if _, err := r.c.List(testCtx(t), "relative/path"); !errors.Is(err, ErrInvalid) {
		t.Errorf("relative path: %v", err)
	}
}

func TestListTruncated(t *testing.T) {
	r := newRig(t)
	dir := t.TempDir()
	for i := 0; i < MaxList+1; i++ {
		f, err := os.Create(filepath.Join(dir, "file-with-a-long-name-"+strings.Repeat("x", 20)+"-"+itoa(i)))
		if err != nil {
			t.Fatal(err)
		}
		f.Close()
	}
	l, err := r.c.List(testCtx(t), dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Entries) != MaxList || !l.Truncated {
		t.Fatalf("got %d entries truncated=%v", len(l.Entries), l.Truncated)
	}
}

func TestStatMkdir(t *testing.T) {
	r := newRig(t)
	ctx := testCtx(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "new")
	if err := r.c.Mkdir(ctx, p); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(p); err != nil || !fi.IsDir() {
		t.Fatalf("mkdir: %v", err)
	}
	if err := r.c.Mkdir(ctx, p); !errors.Is(err, ErrExists) {
		t.Fatalf("second mkdir: %v", err)
	}
	makeFile(t, filepath.Join(dir, "f.bin"), 123, 4)
	e, err := r.c.Stat(ctx, filepath.Join(dir, "f.bin"))
	if err != nil || e.Size != 123 || e.IsDir || e.Name != "f.bin" {
		t.Fatalf("stat: %+v %v", e, err)
	}
	if _, err := r.c.Stat(ctx, filepath.Join(dir, "nope")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stat missing: %v", err)
	}
}

var sizes = []int{0, 1, ChunkSize - 1, ChunkSize, ChunkSize + 1, 2 * ChunkSize, 5<<20 + 3}

func TestDownloadFile(t *testing.T) {
	r := newRig(t)
	src, dst := t.TempDir(), t.TempDir()
	for i, n := range sizes {
		name := "f" + itoa(i) + ".bin"
		want := makeFile(t, filepath.Join(src, name), n, uint64(i))
		os.Chtimes(filepath.Join(src, name), oldTime, oldTime)
		var last Progress
		tr, err := r.c.Download(testCtx(t), filepath.Join(src, name), dst, func(p Progress) { last = p })
		if err != nil {
			t.Fatalf("size %d: %v", n, err)
		}
		res, err := tr.Wait()
		if err != nil {
			t.Fatalf("size %d: Wait: %v", n, err)
		}
		got, err := os.ReadFile(filepath.Join(dst, name))
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("size %d: content mismatch (%v)", n, err)
		}
		if fi, _ := os.Stat(filepath.Join(dst, name)); !fi.ModTime().Equal(oldTime) {
			t.Errorf("size %d: mtime %v", n, fi.ModTime())
		}
		if res.Files != 1 || res.Bytes != int64(n) || res.Name != name {
			t.Errorf("size %d: result %+v", n, res)
		}
		if last.Done != int64(n) || last.Total != int64(n) || last.FilesDone != 1 || last.Files != 1 {
			t.Errorf("size %d: last progress %+v", n, last)
		}
	}
	noParts(t, dst)
}

func TestUploadFile(t *testing.T) {
	r := newRig(t)
	src, dst := t.TempDir(), t.TempDir()
	for i, n := range sizes {
		name := "u" + itoa(i) + ".bin"
		want := makeFile(t, filepath.Join(src, name), n, uint64(100+i))
		os.Chtimes(filepath.Join(src, name), oldTime, oldTime)
		var last Progress
		tr, err := r.c.Upload(testCtx(t), filepath.Join(src, name), dst, func(p Progress) { last = p })
		if err != nil {
			t.Fatalf("size %d: %v", n, err)
		}
		res, err := tr.Wait()
		if err != nil {
			t.Fatalf("size %d: Wait: %v", n, err)
		}
		got, err := os.ReadFile(filepath.Join(dst, name))
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("size %d: content mismatch (%v)", n, err)
		}
		if fi, _ := os.Stat(filepath.Join(dst, name)); !fi.ModTime().Equal(oldTime) {
			t.Errorf("size %d: mtime %v", n, fi.ModTime())
		}
		if res.Files != 1 || res.Bytes != int64(n) || res.Name != name {
			t.Errorf("size %d: result %+v", n, res)
		}
		if last.Done != int64(n) || last.FilesDone != 1 {
			t.Errorf("size %d: last progress %+v", n, last)
		}
	}
	noParts(t, dst)
}

// makeTree builds root/{a.txt, sub/b.bin, sub/deep/c.txt, empty/, link->a.txt}.
func makeTree(t *testing.T, base string) map[string][]byte {
	root := filepath.Join(base, "root")
	m := map[string][]byte{
		"a.txt":          makeFile(t, filepath.Join(root, "a.txt"), 10, 1),
		"sub/b.bin":      makeFile(t, filepath.Join(root, "sub", "b.bin"), 200<<10, 2),
		"sub/deep/c.txt": makeFile(t, filepath.Join(root, "sub", "deep", "c.txt"), 0, 3),
	}
	os.Mkdir(filepath.Join(root, "empty"), 0o755)
	if err := os.Symlink("a.txt", filepath.Join(root, "link")); err != nil && runtime.GOOS != "windows" {
		t.Fatal(err)
	}
	return m
}

func checkTree(t *testing.T, root string, want map[string][]byte) {
	t.Helper()
	for rel, b := range want {
		got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil || !bytes.Equal(got, b) {
			t.Errorf("%s: mismatch (%v)", rel, err)
		}
	}
	if fi, err := os.Stat(filepath.Join(root, "empty")); err != nil || !fi.IsDir() {
		t.Errorf("empty dir missing: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(root, "link")); err == nil {
		t.Errorf("symlink was copied")
	}
}

func TestDownloadDirTree(t *testing.T) {
	r := newRig(t)
	src, dst := t.TempDir(), t.TempDir()
	want := makeTree(t, src)
	for i, root := range []string{"root", "root (1)"} {
		tr, err := r.c.Download(testCtx(t), filepath.Join(src, "root"), dst, nil)
		if err != nil {
			t.Fatal(err)
		}
		res, err := tr.Wait()
		if err != nil {
			t.Fatalf("round %d: %v", i, err)
		}
		if res.Name != root || res.Files != 3 {
			t.Errorf("round %d: result %+v", i, res)
		}
		if runtime.GOOS != "windows" && (len(res.Skipped) != 1 || res.Skipped[0] != "root/link") {
			t.Errorf("skipped %v", res.Skipped)
		}
		checkTree(t, filepath.Join(dst, root), want)
	}
	noParts(t, dst)
}

func TestUploadDirTree(t *testing.T) {
	r := newRig(t)
	src, dst := t.TempDir(), t.TempDir()
	want := makeTree(t, src)
	for i, root := range []string{"root", "root (1)"} {
		tr, err := r.c.Upload(testCtx(t), filepath.Join(src, "root"), dst, nil)
		if err != nil {
			t.Fatal(err)
		}
		res, err := tr.Wait()
		if err != nil {
			t.Fatalf("round %d: %v", i, err)
		}
		if res.Name != root || res.Files != 3 {
			t.Errorf("round %d: result %+v", i, res)
		}
		if runtime.GOOS != "windows" && len(res.Skipped) != 1 {
			t.Errorf("skipped %v", res.Skipped)
		}
		checkTree(t, filepath.Join(dst, root), want)
	}
	noParts(t, dst)
}

func TestConflictRename(t *testing.T) {
	r := newRig(t)
	src, dst := t.TempDir(), t.TempDir()
	makeFile(t, filepath.Join(src, "a.txt"), 5, 1)
	existing := makeFile(t, filepath.Join(dst, "a.txt"), 7, 2)
	for _, want := range []string{"a (1).txt", "a (2).txt"} {
		tr, err := r.c.Download(testCtx(t), filepath.Join(src, "a.txt"), dst, nil)
		if err != nil {
			t.Fatal(err)
		}
		res, err := tr.Wait()
		if err != nil || res.Name != want {
			t.Fatalf("got %+v %v, want %s", res, err, want)
		}
	}
	// Upload into the same directory renames too and never overwrites.
	tr, err := r.c.Upload(testCtx(t), filepath.Join(src, "a.txt"), dst, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res, err := tr.Wait(); err != nil || res.Name != "a (3).txt" {
		t.Fatalf("upload: %+v %v", res, err)
	}
	if got, _ := os.ReadFile(filepath.Join(dst, "a.txt")); !bytes.Equal(got, existing) {
		t.Fatal("existing file overwritten")
	}
}

// corrupt flips one byte in the first data chunk sent from end.
func corrupt(end *PipeEnd) {
	var once sync.Once
	end.tamper = func(isText bool, b []byte) {
		if !isText && len(b) > headerLen && b[0] == kindData {
			once.Do(func() { b[headerLen] ^= 0xff })
		}
	}
}

func TestChecksumMismatch(t *testing.T) {
	t.Run("download", func(t *testing.T) {
		r := newRig(t)
		src, dst := t.TempDir(), t.TempDir()
		makeFile(t, filepath.Join(src, "x.bin"), 300<<10, 9)
		corrupt(r.se)
		tr, err := r.c.Download(testCtx(t), filepath.Join(src, "x.bin"), dst, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tr.Wait(); !errors.Is(err, ErrChecksum) {
			t.Fatalf("Wait: %v, want ErrChecksum", err)
		}
		if ents, _ := os.ReadDir(dst); len(ents) != 0 {
			t.Fatalf("destination not empty: %v", ents)
		}
	})
	t.Run("upload", func(t *testing.T) {
		r := newRig(t)
		src, dst := t.TempDir(), t.TempDir()
		makeFile(t, filepath.Join(src, "x.bin"), 300<<10, 9)
		corrupt(r.ce)
		tr, err := r.c.Upload(testCtx(t), filepath.Join(src, "x.bin"), dst, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tr.Wait(); !errors.Is(err, ErrChecksum) {
			t.Fatalf("Wait: %v, want ErrChecksum", err)
		}
		if ents, _ := os.ReadDir(dst); len(ents) != 0 {
			t.Fatalf("destination not empty: %v", ents)
		}
	})
}

// slow makes every message take d to deliver.
func slow(r *rig, d time.Duration) { r.ce.delay, r.se.delay = d, d }

func TestCancel(t *testing.T) {
	for _, dir := range []string{"download", "upload"} {
		t.Run(dir, func(t *testing.T) {
			r := newRig(t)
			slow(r, 200*time.Microsecond)
			src, dst := t.TempDir(), t.TempDir()
			makeFile(t, filepath.Join(src, "big.bin"), 40<<20, 5)
			started := make(chan struct{})
			var once sync.Once
			progress := func(p Progress) {
				if p.Done > 0 {
					once.Do(func() { close(started) })
				}
			}
			var tr *Transfer
			var err error
			if dir == "download" {
				tr, err = r.c.Download(testCtx(t), filepath.Join(src, "big.bin"), dst, progress)
			} else {
				tr, err = r.c.Upload(testCtx(t), filepath.Join(src, "big.bin"), dst, progress)
			}
			if err != nil {
				t.Fatal(err)
			}
			<-started
			tr.Cancel()
			if _, err := tr.Wait(); !errors.Is(err, ErrCanceled) {
				t.Fatalf("Wait: %v, want ErrCanceled", err)
			}
			eventually(t, "server transfers to stop", func() bool { return r.s.active() == 0 })
			eventually(t, "temporary files removed", func() bool { return countParts(dst) == 0 })
			if ents, _ := os.ReadDir(dst); len(ents) != 0 {
				t.Fatalf("destination not empty: %v", ents)
			}
			// The channel still works.
			if _, err := r.c.List(testCtx(t), src); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPermissionRevoked(t *testing.T) {
	for _, dir := range []string{"download", "upload"} {
		t.Run(dir, func(t *testing.T) {
			r := newRig(t)
			slow(r, 200*time.Microsecond)
			src, dst := t.TempDir(), t.TempDir()
			makeFile(t, filepath.Join(src, "big.bin"), 40<<20, 5)
			var once sync.Once
			progress := func(p Progress) {
				if p.Done > 0 {
					once.Do(func() { r.allowed.Store(false) })
				}
			}
			var tr *Transfer
			var err error
			if dir == "download" {
				tr, err = r.c.Download(testCtx(t), filepath.Join(src, "big.bin"), dst, progress)
			} else {
				tr, err = r.c.Upload(testCtx(t), filepath.Join(src, "big.bin"), dst, progress)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := tr.Wait(); !errors.Is(err, ErrDenied) {
				t.Fatalf("Wait: %v, want ErrDenied", err)
			}
			eventually(t, "server transfers to stop", func() bool { return r.s.active() == 0 })
			eventually(t, "temporary files removed", func() bool { return countParts(dst) == 0 })
			if _, err := r.c.List(testCtx(t), src); !errors.Is(err, ErrDenied) {
				t.Fatalf("List while denied: %v", err)
			}
			r.allowed.Store(true)
			if _, err := r.c.List(testCtx(t), src); err != nil {
				t.Fatalf("List after re-allow: %v", err)
			}
		})
	}
}

func TestDeniedRequests(t *testing.T) {
	r := newRig(t)
	r.allowed.Store(false)
	ctx := testCtx(t)
	dir := t.TempDir()
	if _, err := r.c.Home(ctx); !errors.Is(err, ErrDenied) {
		t.Errorf("Home: %v", err)
	}
	if err := r.c.Mkdir(ctx, filepath.Join(dir, "x")); !errors.Is(err, ErrDenied) {
		t.Errorf("Mkdir: %v", err)
	}
	if _, err := r.c.Download(ctx, dir, t.TempDir(), nil); !errors.Is(err, ErrDenied) {
		t.Errorf("Download: %v", err)
	}
	makeFile(t, filepath.Join(dir, "f"), 1, 1)
	tr, err := r.c.Upload(ctx, filepath.Join(dir, "f"), t.TempDir(), nil)
	if err == nil {
		_, err = tr.Wait()
	}
	if !errors.Is(err, ErrDenied) {
		t.Errorf("Upload: %v", err)
	}
}

func TestDisconnectCleanup(t *testing.T) {
	for _, dir := range []string{"download", "upload"} {
		t.Run(dir, func(t *testing.T) {
			r := newRig(t)
			slow(r, 200*time.Microsecond)
			src, dst := t.TempDir(), t.TempDir()
			makeFile(t, filepath.Join(src, "big.bin"), 40<<20, 5)
			var once sync.Once
			progress := func(p Progress) {
				if p.Done > 1<<20 {
					once.Do(func() { go r.ce.Close() })
				}
			}
			var tr *Transfer
			var err error
			if dir == "download" {
				tr, err = r.c.Download(testCtx(t), filepath.Join(src, "big.bin"), dst, progress)
			} else {
				tr, err = r.c.Upload(testCtx(t), filepath.Join(src, "big.bin"), dst, progress)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := tr.Wait(); !errors.Is(err, ErrClosed) {
				t.Fatalf("Wait: %v, want ErrClosed", err)
			}
			eventually(t, "server transfers to stop", func() bool { return r.s.active() == 0 })
			eventually(t, "temporary files removed", func() bool { return countParts(dst) == 0 })
			if _, err := r.c.List(testCtx(t), src); !errors.Is(err, ErrClosed) {
				t.Fatalf("List after close: %v", err)
			}
		})
	}
}

func TestPartialFailure(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs an unreadable file")
	}
	r := newRig(t)
	src, dst := t.TempDir(), t.TempDir()
	want := makeTree(t, src)
	bad := filepath.Join(src, "root", "sub", "b.bin")
	os.Chmod(bad, 0)
	defer os.Chmod(bad, 0o644)
	delete(want, "sub/b.bin")
	tr, err := r.c.Download(testCtx(t), filepath.Join(src, "root"), dst, nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := tr.Wait()
	var pe *PartialError
	if !errors.As(err, &pe) || pe.Failed != 1 {
		t.Fatalf("Wait: %v, want PartialError", err)
	}
	if len(res.Failed) != 1 || res.Failed[0].Path != "root/sub/b.bin" || !errors.Is(res.Failed[0].Err, ErrPermission) {
		t.Fatalf("failed %+v", res.Failed)
	}
	checkTree(t, filepath.Join(dst, "root"), want)
}

func TestUploadReadOnlyDir(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a read-only directory")
	}
	r := newRig(t)
	src, dst := t.TempDir(), t.TempDir()
	makeFile(t, filepath.Join(src, "a.txt"), 5, 1)
	os.Chmod(dst, 0o555)
	defer os.Chmod(dst, 0o755)
	tr, err := r.c.Upload(testCtx(t), filepath.Join(src, "a.txt"), dst, nil)
	if err == nil {
		_, err = tr.Wait()
	}
	if !errors.Is(err, ErrPermission) {
		t.Fatalf("got %v, want ErrPermission", err)
	}
}

func TestListDuringDownload(t *testing.T) {
	r := newRig(t)
	slow(r, 100*time.Microsecond)
	src, dst := t.TempDir(), t.TempDir()
	makeFile(t, filepath.Join(src, "big.bin"), 30<<20, 5)
	started := make(chan struct{})
	var once sync.Once
	tr, err := r.c.Download(testCtx(t), filepath.Join(src, "big.bin"), dst, func(p Progress) {
		if p.Done > 0 {
			once.Do(func() { close(started) })
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	<-started
	t0 := time.Now()
	if _, err := r.c.List(testCtx(t), src); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(t0); d > 2*time.Second {
		t.Errorf("List took %v during a download", d)
	}
	if _, err := tr.Wait(); err != nil {
		t.Fatal(err)
	}
}

// TestMemoryBounded streams 300 MB through the in-memory pipe and checks the
// heap stays small, i.e. flow control keeps the sender from queuing the file.
func TestMemoryBounded(t *testing.T) {
	if testing.Short() {
		t.Skip("large transfer")
	}
	r := newRig(t)
	src, dst := t.TempDir(), t.TempDir()
	const size = 300 << 20
	f, err := os.Create(filepath.Join(src, "huge.bin"))
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 1<<20)
	for i := range buf {
		buf[i] = byte(i * 31)
	}
	for i := 0; i < size/len(buf); i++ {
		buf[0] = byte(i)
		f.Write(buf)
	}
	f.Close()

	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	base := ms.HeapAlloc
	var peak atomic.Uint64
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		var m runtime.MemStats
		for {
			select {
			case <-stop:
				return
			case <-time.After(20 * time.Millisecond):
			}
			runtime.ReadMemStats(&m)
			if m.HeapAlloc > peak.Load() {
				peak.Store(m.HeapAlloc)
			}
		}
	}()
	var maxBuffered atomic.Uint64
	t0 := time.Now()
	tr, err := r.c.Download(testCtx(t), filepath.Join(src, "huge.bin"), dst, func(Progress) {
		if b := r.se.BufferedAmount(); b > maxBuffered.Load() {
			maxBuffered.Store(b)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := tr.Wait()
	close(stop)
	wg.Wait()
	if err != nil {
		t.Fatal(err)
	}
	el := time.Since(t0)
	growth := int64(peak.Load()) - int64(base)
	t.Logf("300 MB in %v (%.0f MB/s); peak heap growth %.1f MB; max buffered %.1f MB",
		el, float64(res.Bytes)/el.Seconds()/1e6, float64(growth)/1e6, float64(maxBuffered.Load())/1e6)
	if growth > 64<<20 {
		t.Errorf("heap grew by %d MB", growth>>20)
	}
	if maxBuffered.Load() > highWater+2*maxMessage {
		t.Errorf("buffered amount reached %d", maxBuffered.Load())
	}
	if fi, err := os.Stat(filepath.Join(dst, "huge.bin")); err != nil || fi.Size() != size {
		t.Fatalf("result file: %v", err)
	}
}

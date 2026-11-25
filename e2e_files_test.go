package main

// End-to-end test of the 文件传输 window: a host and a files-only viewer of
// this binary on one machine, driven through Tango's automation channel
// (no OS input, windows stay in the background). It needs a graphical
// session and screen recording permission for the host, so it only runs
// with TANGO_E2E=1 and KKDESK_E2E_FILES=<dir>, a scratch directory with
// src/big.bin, src/huge.bin, src/tree/ and empty dst-remote/, dst-local/:
//
//	TANGO_E2E=1 KKDESK_E2E_FILES=/tmp/kkdesk-f2 go test -run E2EFiles -v .
//
// KKDESK_E2E_SHOTS=<dir> saves screenshots of the windows there.

import (
	"crypto/sha256"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tangoui/tango/automation"
	"github.com/tangoui/tango/ui/a11y"
)

func TestE2EFiles(t *testing.T) {
	root := os.Getenv("KKDESK_E2E_FILES")
	if root == "" {
		t.Skip("KKDESK_E2E_FILES not set")
	}
	shots := os.Getenv("KKDESK_E2E_SHOTS")
	shot := func(w *automation.Window, name string) {
		if shots != "" {
			w.SaveScreenshot(filepath.Join(shots, name))
		}
	}
	src, dstR, dstL := filepath.Join(root, "src"), filepath.Join(root, "dst-remote"), filepath.Join(root, "dst-local")

	host := automation.Launch(t, ".", "-host", "-listen", ":21918", "-autoaccept", "-password", "123456", "-applog", "")
	host.SetTimeout(30 * time.Second)
	hw := host.Window("kkdesk")
	hw.FindContains(a11y.RoleNone, "等待连接").Wait()

	viewer := automation.Launch(t, ".", "-connect", "127.0.0.1:21918", "-password", "123456", "-files", "-applog", "")
	viewer.SetTimeout(30 * time.Second)
	fw := viewer.Window("文件传输 - 127.0.0.1:21918")
	defer func() {
		if t.Failed() {
			t.Logf("viewer output:\n%s", viewer.Output())
			t.Logf("host output:\n%s", host.Output())
		}
	}()

	// Both panes list their home folders.
	home, _ := os.UserHomeDir()
	fw.Find(a11y.RoleTextbox, "本机路径").WaitValue(home)
	fw.Find(a11y.RoleTextbox, "对方路径").WaitValue(home)

	// goTo types a path into a pane's path field and submits it.
	goTo := func(field, path string) {
		t.Helper()
		f := fw.Find(a11y.RoleTextbox, field)
		f.SetValue(path)
		f.Focus()
		fw.Press("Enter")
		f.WaitValue(path)
	}
	// row returns the n-th row named name (0 = local pane, 1 = remote).
	row := func(name string, n int) *automation.Locator { return fw.Find(a11y.RoleRow, name).Nth(n) }
	dbl := func(l *automation.Locator) {
		t.Helper()
		b := l.Bounds()
		fw.DoubleClick(b.X+b.W/2, b.Y+b.H/2)
	}

	// Browse by double-clicking folders.
	goTo("本机路径", root)
	goTo("对方路径", root)
	dbl(row("src", 0))
	fw.Find(a11y.RoleTextbox, "本机路径").WaitValue(src)
	dbl(row("dst-remote", 1))
	fw.Find(a11y.RoleTextbox, "对方路径").WaitValue(dstR)
	row("tree", 0).Wait()
	shot(fw, "files-idle.png")
	shot(hw, "files-controlled.png")

	done := func(n int) {
		t.Helper()
		deadline := time.Now().Add(60 * time.Second)
		for fw.FindContains(a11y.RoleText, "完成 ·").Count() < n {
			if time.Now().After(deadline) {
				t.Fatalf("transfer %d did not finish:\n%s", n, fw.Dump())
			}
			time.Sleep(100 * time.Millisecond)
		}
	}

	// Upload a folder.
	row("tree", 0).ClickPointer()
	fw.Find(a11y.RoleButton, "上传 →").Click()
	done(1)
	row("tree", 1).Wait() // the remote pane was refreshed
	compareTrees(t, filepath.Join(src, "tree"), filepath.Join(dstR, "tree"))

	// Download a 200 MB file into dst-local.
	goTo("本机路径", dstL)
	goTo("对方路径", src)
	row("big.bin", 0).ClickPointer() // only the remote pane lists it
	fw.Find(a11y.RoleButton, "← 下载").Click()
	fw.FindContains(a11y.RoleText, "剩余").Timeout(20 * time.Second).Wait()
	shot(fw, "files-transfer.png")
	done(2)
	row("big.bin", 0).Wait() // local pane refreshed
	if a, b := sum(t, filepath.Join(src, "big.bin")), sum(t, filepath.Join(dstL, "big.bin")); a != b {
		t.Fatalf("big.bin sha256 %x != %x", b, a)
	}
	shot(fw, "files-done.png")

	// Cancel a 2 GB download midway, then a 2 GB upload.
	cancelMidway := func(rowN int, button string) {
		t.Helper()
		row("huge.bin", rowN).ClickPointer()
		fw.Find(a11y.RoleButton, button).Click()
		fw.FindContains(a11y.RoleText, "剩余").Timeout(20 * time.Second).Wait()
		time.Sleep(300 * time.Millisecond)
		fw.Find(a11y.RoleButton, "取消").Click()
		fw.FindContains(a11y.RoleText, "已取消").Timeout(20 * time.Second).Wait()
	}
	cancelMidway(0, "← 下载")
	// Upload huge.bin from src (local) into dst-remote.
	goTo("本机路径", src)
	goTo("对方路径", dstR)
	cancelMidway(0, "上传 →")
	time.Sleep(500 * time.Millisecond) // the host deletes its partial file on the cancel message
	for _, d := range []string{dstL, dstR} {
		if left := partFiles(t, d); len(left) > 0 {
			t.Fatalf("partial files left after cancel: %v", left)
		}
		if _, err := os.Stat(filepath.Join(d, "huge.bin")); err == nil {
			t.Fatalf("canceled huge.bin exists in %s", d)
		}
	}

	// The host turns file access off during a transfer.
	row("huge.bin", 0).ClickPointer()
	fw.Find(a11y.RoleButton, "上传 →").Click()
	fw.FindContains(a11y.RoleText, "剩余").Timeout(20 * time.Second).Wait()
	chip := hw.Find(a11y.RoleSwitch, "文件传输")
	chip.Click()
	fw.FindContains(a11y.RoleText, "对方已关闭文件访问").Timeout(10 * time.Second).Wait()
	time.Sleep(time.Second) // the message has faded in
	shot(fw, "files-denied.png")
	shot(hw, "files-controlled-off.png")
	time.Sleep(500 * time.Millisecond)
	if left := partFiles(t, dstR); len(left) > 0 {
		t.Fatalf("partial files left after denial: %v", left)
	}
	// Listing is denied too, until access is back on.
	fw.Find(a11y.RoleButton, "刷新").Click()
	chip.Click()
	fw.Find(a11y.RoleButton, "刷新").Click()
	row("tree", 1).Wait()
	t.Logf("viewer log:\n%s", grepLines(viewer.Output(), "files:"))
	t.Logf("host log:\n%s", grepLines(host.Output(), "files:", "file access"))
}

func sum(t *testing.T, path string) [32]byte {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		t.Fatal(err)
	}
	return [32]byte(h.Sum(nil))
}

// compareTrees checks that b has the same files with the same content as a.
func compareTrees(t *testing.T, a, b string) {
	t.Helper()
	n := 0
	err := filepath.WalkDir(a, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(a, p)
		fi, err := os.Stat(filepath.Join(b, rel))
		if err != nil {
			return err
		}
		if d.IsDir() != fi.IsDir() {
			t.Errorf("%s: dir mismatch", rel)
		}
		if !d.IsDir() {
			n++
			if sum(t, p) != sum(t, filepath.Join(b, rel)) {
				t.Errorf("%s: content differs", rel)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("folder upload: %d files identical", n)
}

func partFiles(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && strings.HasSuffix(d.Name(), ".kkdesk-part") {
			out = append(out, p)
		}
		return nil
	})
	return out
}

func grepLines(s string, subs ...string) string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		for _, sub := range subs {
			if strings.Contains(l, sub) {
				out = append(out, l)
				break
			}
		}
	}
	return strings.Join(out, "\n")
}

package files

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// emptySHA is the SHA-256 of zero bytes.
const emptySHA = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

// feedDownload runs events through a download transfer expecting want and
// returns its result and the destination directory.
func feedDownload(t *testing.T, want string, files int, events []msg) (Result, error, string) {
	t.Helper()
	r, dir := newRig(t), t.TempDir()
	root, err := openReceiveRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	tr := r.c.newTransfer(testCtx(t), false, root, nil)
	t.Cleanup(tr.Cancel)
	tr.want = want
	tr.meter.update(true, func(p *Progress) { p.Files = files })
	for _, e := range events {
		tr.handle(&e)
	}
	res, err := tr.Wait()
	return res, err, dir
}

func emptyFile(rel string, mode uint32) []msg {
	return []msg{{Op: "file", Rel: rel, Mode: mode}, {Op: "end", Rel: rel, SHA256: emptySHA}}
}

func TestDownloadRejectsContentOutsideTheRequest(t *testing.T) {
	cat := func(parts ...[]msg) []msg {
		var all []msg
		for _, p := range parts {
			all = append(all, p...)
		}
		return all
	}
	for _, tc := range []struct {
		name   string
		want   string
		events []msg
		absent []string
	}{
		{"file under another name", "report.pdf", cat(emptyFile(".zshenv", 0o755), []msg{{Op: "done"}}), []string{".zshenv"}},
		{"second file after a single file", "report.pdf",
			cat(emptyFile("report.pdf", 0o644), emptyFile("evil.command", 0o755), []msg{{Op: "done"}}), []string{"evil.command"}},
		{"folder under another name", "photos", []msg{{Op: "dir", Rel: "other"}, {Op: "done"}}, []string{"other"}},
		{"top-level file next to the folder", "photos",
			cat([]msg{{Op: "dir", Rel: "photos"}}, emptyFile(".zshenv", 0o755), []msg{{Op: "done"}}), []string{".zshenv"}},
		{"second top-level folder", "photos", []msg{{Op: "dir", Rel: "photos"}, {Op: "dir", Rel: "other"}, {Op: "done"}}, []string{"other"}},
		{"folder content before the folder", "photos", cat(emptyFile("photos/a.txt", 0o644), []msg{{Op: "done"}}), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err, dir := feedDownload(t, tc.want, 3, tc.events)
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("Wait error = %v, want ErrInvalid", err)
			}
			for _, name := range tc.absent {
				if _, err := os.Lstat(filepath.Join(dir, name)); err == nil {
					t.Errorf("%s was created in the download directory", name)
				}
			}
		})
	}
}

func TestDownloadAcceptsNestedFolder(t *testing.T) {
	events := []msg{{Op: "dir", Rel: "photos"}, {Op: "dir", Rel: "photos/2026"}}
	events = append(events, emptyFile("photos/2026/a.txt", 0o644)...)
	events = append(events, emptyFile("photos/b.txt", 0o644)...)
	events = append(events, msg{Op: "done"})
	res, err, dir := feedDownload(t, "photos", 2, events)
	if err != nil || res.Files != 2 {
		t.Fatalf("Wait = %+v, %v; want 2 files", res, err)
	}
	for _, p := range []string{"photos/2026/a.txt", "photos/b.txt"} {
		if _, err := os.Stat(filepath.Join(dir, p)); err != nil {
			t.Errorf("%s: %v", p, err)
		}
	}
}

func TestDownloadDropsExecutableBits(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no executable bits on Windows")
	}
	_, err, dir := feedDownload(t, "tool", 1, append(emptyFile("tool", 0o755), msg{Op: "done"}))
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(dir, "tool"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm()&0o111 != 0 {
		t.Fatalf("downloaded file mode %v keeps executable bits", fi.Mode().Perm())
	}
}

func TestDownloadMarksFilesFromTheInternet(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Zone.Identifier is Windows-only (macOS quarantine is set through xattr)")
	}
	_, err, dir := feedDownload(t, "report.pdf", 1, append(emptyFile("report.pdf", 0o644), msg{Op: "done"}))
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "report.pdf") + ":Zone.Identifier")
	if err != nil || !strings.Contains(string(b), "ZoneId=3") {
		t.Fatalf("Zone.Identifier = %q, %v; want ZoneId=3", b, err)
	}
}

func TestReplyNameMatchesRequest(t *testing.T) {
	for _, tc := range []struct {
		path, name string
		ok         bool
	}{
		{"/home/a/report.pdf", "report.pdf", true},
		{`C:\Users\a\report.pdf`, "report.pdf", true},
		{"/home/a/photos/", "photos", true},
		{`D:\photos\`, "photos", true},
		{"/home/a/report.pdf", ".zshenv", false},
		{"/home/a/myreport.pdf", "report.pdf", false},
		{"/home/a/x", "a/x", false},
		{"/home/a/x", "", false},
		{"/home/a/x", "..", false},
	} {
		if got := replyNameMatches(tc.path, tc.name); got != tc.ok {
			t.Errorf("replyNameMatches(%q, %q) = %v, want %v", tc.path, tc.name, got, tc.ok)
		}
	}
}

// A host cannot show one name and hand out another path: the viewer
// downloads Entry.Path and the reply name is checked against its last
// element, so that element must be the name the user sees.
func TestListingDropsEntriesWhoseNameHidesThePath(t *testing.T) {
	l := checkedListing(Listing{Path: "/home/a", Entries: []Entry{
		{Name: "report.pdf", Path: "/home/a/report.pdf"},
		{Name: "report.pdf", Path: "/tmp/.zshenv"},
		{Name: "Photos", Path: "/home/a/.config", IsDir: true},
		{Name: "a/b", Path: "/home/a/a/b"},
		{Name: "doc.txt", Path: `C:\Users\a\doc.txt`},
	}})
	var got []string
	for _, e := range l.Entries {
		got = append(got, e.Path)
	}
	want := []string{"/home/a/report.pdf", `C:\Users\a\doc.txt`}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("entries = %q, want %q", got, want)
	}
}

package files

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSanitizeName(t *testing.T) {
	cases := []struct {
		goos, in, want string
	}{
		{"windows", "a:b*c?d\"e<f>g|h", "a_b_c_d_e_f_g_h"},
		{"windows", `back\slash`, "back_slash"},
		{"windows", "ctl\x01x", "ctl_x"},
		{"windows", "trailing. ", "trailing__"},
		{"windows", "CON", "_CON"},
		{"windows", "con.txt", "_con.txt"},
		{"windows", "COM1.log", "_COM1.log"},
		{"windows", "LPT9", "_LPT9"},
		{"windows", "CONSOLE", "CONSOLE"},
		{"windows", "普通 文件.txt", "普通 文件.txt"},
		{"darwin", "a:b*c?d.txt", "a:b*c?d.txt"},
		{"linux", `back\slash`, `back\slash`},
		{"linux", "a\x00b", "a_b"},
	}
	for _, c := range cases {
		if got := SanitizeName(c.in, c.goos); got != c.want {
			t.Errorf("SanitizeName(%q, %s) = %q, want %q", c.in, c.goos, got, c.want)
		}
	}
}

func TestSplitRel(t *testing.T) {
	good := map[string][]string{
		"a.txt":       {"a.txt"},
		"dir/sub/x.y": {"dir", "sub", "x.y"},
	}
	for in, want := range good {
		got, err := splitRel(in, "linux")
		if err != nil || len(got) != len(want) {
			t.Fatalf("splitRel(%q) = %v, %v", in, got, err)
		}
		for i := range got {
			if got[i] != want[i] {
				t.Fatalf("splitRel(%q) = %v, want %v", in, got, want)
			}
		}
	}
	for _, bad := range []string{"", "/abs", "a//b", "../x", "a/../b", "a/.", "a/"} {
		if _, err := splitRel(bad, "linux"); err == nil {
			t.Errorf("splitRel(%q) accepted", bad)
		}
	}
	// On Windows a backslash or drive colon is replaced, never a separator.
	got, err := splitRel(`..\..\x`, "windows")
	if err != nil || len(got) != 1 || got[0] != `.._.._x` {
		t.Fatalf("windows splitRel = %v, %v", got, err)
	}
}

func TestUniqueName(t *testing.T) {
	dir := t.TempDir()
	if got := uniqueName(dir, "a.txt"); got != "a.txt" {
		t.Fatalf("free name: %q", got)
	}
	os.WriteFile(filepath.Join(dir, "a.txt"), nil, 0o644)
	if got := uniqueName(dir, "a.txt"); got != "a (1).txt" {
		t.Fatalf("first conflict: %q", got)
	}
	os.WriteFile(filepath.Join(dir, "a (1).txt"), nil, 0o644)
	if got := uniqueName(dir, "a.txt"); got != "a (2).txt" {
		t.Fatalf("second conflict: %q", got)
	}
	os.Mkdir(filepath.Join(dir, "photos"), 0o755)
	if got := uniqueName(dir, "photos"); got != "photos (1)" {
		t.Fatalf("dir conflict: %q", got)
	}
	os.WriteFile(filepath.Join(dir, ".bashrc"), nil, 0o644)
	if got := uniqueName(dir, ".bashrc"); got != ".bashrc (1)" {
		t.Fatalf("dotfile conflict: %q", got)
	}
	os.WriteFile(filepath.Join(dir, "x.tar.gz"), nil, 0o644)
	if got := uniqueName(dir, "x.tar.gz"); got != "x.tar (1).gz" {
		t.Fatalf("double ext: %q", got)
	}
}

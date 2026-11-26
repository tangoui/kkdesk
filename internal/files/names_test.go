package files

import (
	"os"
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

func TestMkdirUnique(t *testing.T) {
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	for _, name := range []string{"a.txt", "a (1).txt", ".bashrc", "x.tar.gz"} {
		if err := root.WriteFile(name, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct{ name, want string }{
		{"a.txt", "a (2).txt"},
		{"photos", "photos"},
		{"photos", "photos (1)"},
		{".bashrc", ".bashrc (1)"},
		{"x.tar.gz", "x.tar (1).gz"},
	} {
		if got, err := mkdirUnique(root, tc.name); err != nil || got != tc.want {
			t.Errorf("mkdirUnique(%q) = %q, %v, want %q", tc.name, got, err, tc.want)
		}
	}
}

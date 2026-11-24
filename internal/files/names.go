package files

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// goos is the platform whose file name rules apply to files written here.
var goos = runtime.GOOS

func itoa(i int) string { return strconv.Itoa(i) }

// SanitizeName makes one path component valid for writing on goos. On
// Windows the characters <>:"/\|?* and control characters become "_", a
// trailing dot or space becomes "_", and reserved device names (CON, NUL,
// COM1, ...) get a "_" prefix. Elsewhere only NUL and "/" are replaced.
func SanitizeName(name, goos string) string {
	if goos != "windows" {
		return strings.Map(func(r rune) rune {
			if r == 0 || r == '/' {
				return '_'
			}
			return r
		}, name)
	}
	s := []rune(strings.Map(func(r rune) rune {
		if r < 32 || strings.ContainsRune(`<>:"/\|?*`, r) {
			return '_'
		}
		return r
	}, name))
	for i := len(s) - 1; i >= 0 && (s[i] == '.' || s[i] == ' '); i-- {
		s[i] = '_'
	}
	out := string(s)
	base := strings.ToUpper(out)
	if i := strings.IndexByte(base, '.'); i >= 0 {
		base = base[:i]
	}
	base = strings.TrimRight(base, " ")
	switch base {
	case "CON", "PRN", "AUX", "NUL":
		return "_" + out
	}
	if len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '1' && base[3] <= '9' {
		return "_" + out
	}
	return out
}

// splitRel validates a "/"-separated relative path from the other side and
// returns its components, each sanitized for goos. It rejects absolute
// paths, empty components, "." and "..".
func splitRel(rel, goos string) ([]string, error) {
	if rel == "" || strings.HasPrefix(rel, "/") {
		return nil, errInvalidPath(rel)
	}
	parts := strings.Split(rel, "/")
	for i, p := range parts {
		if p == "" || p == "." || p == ".." {
			return nil, errInvalidPath(rel)
		}
		parts[i] = SanitizeName(p, goos)
	}
	return parts, nil
}

func errInvalidPath(p string) error {
	return &RemoteError{Code: codeInvalid, Detail: "bad relative path " + strconv.Quote(p)}
}

// splitExt splits name into stem and extension for conflict renaming; a
// leading dot (".bashrc") is part of the stem.
func splitExt(name string) (stem, ext string) {
	i := strings.LastIndexByte(name, '.')
	if i <= 0 {
		return name, ""
	}
	return name[:i], name[i:]
}

// uniqueName returns name, or "stem (n).ext" with the smallest n such that
// nothing named so exists in dir.
func uniqueName(dir, name string) string {
	if !exists(filepath.Join(dir, name)) {
		return name
	}
	stem, ext := splitExt(name)
	for n := 1; ; n++ {
		c := stem + " (" + strconv.Itoa(n) + ")" + ext
		if !exists(filepath.Join(dir, c)) {
			return c
		}
	}
}

func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil || !errors.Is(err, os.ErrNotExist)
}

// absPath validates and cleans an absolute path from the client.
func absPath(p string) (string, error) {
	if p == "" {
		return "", &RemoteError{Code: codeInvalid, Detail: "empty path"}
	}
	c := filepath.Clean(p)
	if len(c) == 2 && c[1] == ':' { // "C:" means the drive root
		c += `\`
	}
	if !filepath.IsAbs(c) {
		return "", &RemoteError{Code: codeInvalid, Detail: "not an absolute path: " + strconv.Quote(p)}
	}
	return c, nil
}

// joinRel joins sanitized components under base.
func joinRel(base string, parts []string) string {
	return filepath.Join(append([]string{base}, parts...)...)
}

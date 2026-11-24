//go:build !windows

package files

import (
	"io/fs"
	"strings"
)

func isHidden(fi fs.FileInfo) bool { return strings.HasPrefix(fi.Name(), ".") }

func volumes() []string { return []string{"/"} }

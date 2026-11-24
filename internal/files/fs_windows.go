package files

import (
	"io/fs"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
)

func isHidden(fi fs.FileInfo) bool {
	if strings.HasPrefix(fi.Name(), ".") {
		return true
	}
	if a, ok := fi.Sys().(*syscall.Win32FileAttributeData); ok {
		return a.FileAttributes&windows.FILE_ATTRIBUTE_HIDDEN != 0
	}
	return false
}

// volumes lists the available drive roots, like `C:\`.
func volumes() []string {
	mask, err := windows.GetLogicalDrives()
	if err != nil || mask == 0 {
		return []string{`C:\`}
	}
	var v []string
	for i := 0; i < 26; i++ {
		if mask&(1<<i) != 0 {
			v = append(v, string(rune('A'+i))+`:\`)
		}
	}
	return v
}

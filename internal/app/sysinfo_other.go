//go:build !darwin && !(windows && cgo)

package app

func platformInfo() []string {
	return []string{"os: no detailed system information on this platform/build"}
}

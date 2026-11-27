//go:build !windows || !amd64 || !cgo

package lockscreen

func PlatformStart() StartFunc { return nil }

func Dispatch(_ []string) (bool, error) { return false, nil }

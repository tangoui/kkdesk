//go:build !windows || !amd64 || !cgo

package lockscreen

func PlatformStart() StartFunc { return nil }

func Dispatch(_ []string) (bool, error) { return false, nil }

func Component() (ComponentInfo, error) { return ComponentInfo{}, nil }

func ManageComponent(bool) error { return ErrUnavailable }

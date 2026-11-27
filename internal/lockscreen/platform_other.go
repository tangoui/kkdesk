//go:build !cgo || (!darwin && (!windows || !amd64))

package lockscreen

func PlatformStart() StartFunc { return nil }

func Dispatch(_ []string) (bool, error) { return false, nil }

func Component() (ComponentInfo, error) { return ComponentInfo{}, nil }

func ManageComponent(bool) error { return ErrUnavailable }

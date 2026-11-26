//go:build windows && cgo

package privacy

import (
	"errors"
	"testing"
)

func TestWindowsScreenAvailability(t *testing.T) {
	fail := errors.New("display affinity rejected")
	opened := false
	s := &windowsScreen{build: 19040, open: func() error { opened = true; return fail }}
	if err := s.Show(nil, func(error) {}); err == nil || opened {
		t.Fatalf("old build tried to open a privacy screen: opened=%v, err=%v", opened, err)
	}
	s.build = 19041
	if err := s.Show(nil, func(error) {}); !opened || !errors.Is(err, fail) {
		t.Fatalf("display-affinity failure not propagated: opened=%v, err=%v", opened, err)
	}
}

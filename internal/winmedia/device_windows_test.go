//go:build windows && cgo

package winmedia

import "testing"

// ResetDevice makes the next Device call create a current device (a display
// change leaves the old one's adapter listing stale outputs).
func TestResetDeviceGivesNewGeneration(t *testing.T) {
	d1, _, g1, err := Device()
	if err != nil {
		t.Skipf("no D3D11 device: %v", err)
	}
	ResetDevice()
	d2, _, g2, err := Device()
	if err != nil {
		t.Fatal(err)
	}
	if g2 == g1 || d2 == d1 {
		t.Fatalf("after ResetDevice: generation %d -> %d, device %#x -> %#x; want a new device", g1, g2, d1, d2)
	}
}

func TestGeneration(t *testing.T) {
	if _, _, _, err := Device(); err != nil {
		t.Skipf("no D3D11 device: %v", err)
	}
	g := Generation()
	ResetDevice()
	if Generation() == g {
		t.Fatal("Generation did not change after ResetDevice")
	}
}

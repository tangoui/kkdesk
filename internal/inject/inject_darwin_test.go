//go:build darwin && cgo

package inject

import "testing"

// No events are posted here: real injection would move the local pointer.
func TestNewReadsMainDisplay(t *testing.T) {
	in, err := New()
	if err != nil {
		t.Fatal(err)
	}
	b := in.Bounds()
	if b.W <= 0 || b.H <= 0 {
		t.Fatalf("main display bounds = %+v", b)
	}
	in.Refresh()
	t.Logf("main display %+v, Accessibility trusted: %v, double-click %v", in.Bounds(), HasPermission(), in.dblClick)
}

package appui

import (
	"testing"

	"github.com/tangoui/tango/ui"
	"github.com/tangoui/tango/ui/input"
)

// The countdown ticks while the user clicks: a press on 接受 at 28 seconds
// and the release at 27 is one click. The button's label was the countdown
// ("接受（28）"), so it became another button every second and such clicks
// were lost; it took several clicks to accept.
func TestConfirmFooterClickAcrossTick(t *testing.T) {
	left, accepted := 28, 0
	d := ui.NewDriver(func(c *ui.Context) {
		if _, accept := confirmFooter(c, "拒绝", "接受", "自动拒绝", left); accept {
			accepted++
		}
	}, ui.DriverOptions{})
	d.Frame()
	r, ok := d.Find("接受")
	if !ok {
		t.Fatal("no 接受 button")
	}
	p := r.Center()
	d.Inject(input.Pointer{Kind: input.Move, Pos: p})
	d.Inject(input.Pointer{Kind: input.Press, Pos: p})
	d.Frame()
	left = 27 // the second ticks between press and release
	d.Frame()
	d.Inject(input.Pointer{Kind: input.Release, Pos: p})
	d.Settle()
	if accepted != 1 {
		t.Fatalf("accepted %d times, want 1", accepted)
	}
	if _, ok := d.FindText("27"); !ok {
		t.Fatal("the countdown is not shown")
	}
}

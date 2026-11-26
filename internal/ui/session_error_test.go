package appui

import (
	"strings"
	"testing"

	"github.com/tangoui/tango/ui"
	"github.com/tangoui/tango/ui/a11y"
	"github.com/tangoui/tango/ui/layout"
	"github.com/tangoui/tango/uihost"
)

func TestScreenErrorVisibleOverRemoteScreen(t *testing.T) {
	view := sessionViewport{}
	d := ui.NewDriver(func(c *ui.Context) {
		ui.Column(c, func() {
			view.render(c, c.Size(), 1920, 1080, func(_ ui.ID, opts ...layout.Option) {
				ui.NativeView(c, "remote", opts...)
				screenError(c, uihost.ErrNoNativeHost)
			})
		}, layout.Grow(1))
	}, ui.DriverOptions{Width: 700, Height: 500, Accessibility: true})
	d.Settle()
	var msg *a11y.Node
	for _, n := range d.A11yFindAll(a11y.RoleText, "") {
		if strings.Contains(n.Name, "无法显示远程画面") {
			msg = n
		}
	}
	if msg == nil {
		t.Fatal("no error message in the accessibility tree")
	}
	b := msg.Bounds
	if b.W <= 0 || b.H <= 0 || b.X < 0 || b.Y < 0 || b.X+b.W > 700 || b.Y+b.H > 500 {
		t.Fatalf("error message bounds %+v are outside the 700x500 window (hidden under the remote screen)", b)
	}
	if !strings.Contains(msg.Name, "tan dev") || !strings.Contains(msg.Name, "正式") {
		t.Fatalf("message %q does not explain that tan dev cannot show the remote screen", msg.Name)
	}
}

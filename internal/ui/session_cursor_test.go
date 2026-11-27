package appui

import (
	"testing"

	"github.com/tangoui/tango/ui"
	"github.com/tangoui/tango/ui/geom"
	tin "github.com/tangoui/tango/ui/input"
	"github.com/tangoui/tango/ui/layout"

	"github.com/tangoui/kkdesk/internal/app"
	"github.com/tangoui/kkdesk/internal/session"
)

func TestSessionScreenCursor(t *testing.T) {
	for _, viewOnly := range []bool{false, true} {
		name := "control"
		if viewOnly {
			name = "view only"
		}
		t.Run(name, func(t *testing.T) {
			w := sessionWin{v: app.NewViewer(app.ViewerConfig{Addr: "192.0.2.1", ViewOnly: viewOnly}, nil)}
			st := app.ViewerState{Phase: app.ViewerStreaming, ViewOnly: viewOnly}
			d := ui.NewDriver(func(c *ui.Context) {
				body := ui.Div(c, layout.W(200), layout.H(200))
				body.Body(func() {
					ui.Div(c, layout.W(200), layout.H(50)).Interact(tin.CursorPointer)
					w.screen(c, st, body.ID(), 200, 100, layout.W(200), layout.H(100))
				})
			}, ui.DriverOptions{Width: 200, Height: 200})
			d.Frame()
			d.Move(geom.Pt(100, 100))

			for _, tc := range []struct {
				shape session.CursorShape
				want  tin.Cursor
			}{
				{session.CursorDefault, tin.CursorDefault},
				{session.CursorPointer, tin.CursorPointer},
				{session.CursorText, tin.CursorText},
				{session.CursorNotAllowed, tin.CursorNotAllowed},
				{session.CursorResizeEW, tin.CursorResizeEW},
				{session.CursorResizeNS, tin.CursorResizeNS},
				{session.CursorMove, tin.CursorMove},
				{session.CursorGrab, tin.CursorGrab},
				{session.CursorGrabbing, tin.CursorGrabbing},
				{0xff, tin.CursorDefault},
				{session.CursorText, tin.CursorText},
			} {
				st.Cursor = tc.shape
				d.Frame()
				if got := d.Cursor(); got != tc.want {
					t.Errorf("remote cursor for shape %v = %v, want %v", tc.shape, got, tc.want)
				}
			}
			d.Move(geom.Pt(100, 25))
			d.Frame()
			if got := d.Cursor(); got != tin.CursorPointer {
				t.Fatalf("toolbar cursor = %v, want pointer", got)
			}
			st.Phase = app.ViewerClosed
			d.Move(geom.Pt(100, 100))
			d.Frame()
			if got := d.Cursor(); got != tin.CursorDefault {
				t.Fatalf("closed screen cursor = %v, want arrow", got)
			}
		})
	}
}

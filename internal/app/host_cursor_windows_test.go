package app

import (
	"testing"

	"github.com/tangoui/kkdesk/internal/session"
)

func TestWindowsCursorMapping(t *testing.T) {
	// A fake LoadCursor makes the resource-to-shape mapping testable without
	// the desktop, and gives a different handle from each resource ID.
	load := func(id uintptr) uintptr { return id * 2 }
	handles := windowsCursorHandles(load)
	for id, want := range map[uintptr]session.CursorShape{
		32512: session.CursorDefault,
		32649: session.CursorPointer,
		32513: session.CursorText,
		32648: session.CursorNotAllowed,
		32644: session.CursorResizeEW,
		32645: session.CursorResizeNS,
		32646: session.CursorMove,
		32514: session.CursorDefault, // wait is not a supported shape
	} {
		if got := windowsCursorShape(windowsCursorShowing, load(id), handles); got != want {
			t.Errorf("cursor resource %d maps to %v, want %v", id, got, want)
		}
	}
	if got := windowsCursorShape(0, load(32649), handles); got != session.CursorDefault {
		t.Errorf("hidden hand maps to %v, want arrow", got)
	}
}

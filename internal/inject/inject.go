// Package inject posts remote input events (mouse, keyboard, wheel) to the
// local session. On macOS it uses CGEventPost and needs the Accessibility
// permission.
package inject

import (
	"errors"
	"time"
)

// ErrUnsupported is returned on platforms without an injection backend.
var ErrUnsupported = errors.New("inject: unsupported platform")

// Rect is a display rectangle in global display coordinates (points, origin
// at the top-left of the main display, y down), as CGDisplayBounds returns.
type Rect struct{ X, Y, W, H float64 }

// toGlobal maps normalized coordinates (clamped to [0,1]) on display b to a
// global point. The point is kept on the display: x <= X+W-1, y <= Y+H-1.
func toGlobal(nx, ny float32, b Rect) (x, y float64) {
	fx := max(0, min(1, float64(nx)))
	fy := max(0, min(1, float64(ny)))
	x = b.X + fx*b.W
	y = b.Y + fy*b.H
	if b.W >= 1 {
		x = min(x, b.X+b.W-1)
	}
	if b.H >= 1 {
		y = min(y, b.Y+b.H-1)
	}
	return x, y
}

// CGEventFlags bits: generic (kCGEventFlagMask*) and device-dependent
// left/right bits (NX_DEVICE*KEYMASK from IOLLEvent.h), which some apps use to
// tell the left and right modifier keys apart.
const (
	flagShift     uint64 = 0x00020000
	flagControl   uint64 = 0x00040000
	flagAlternate uint64 = 0x00080000
	flagCommand   uint64 = 0x00100000

	devLCtl   uint64 = 0x00000001
	devLShift uint64 = 0x00000002
	devRShift uint64 = 0x00000004
	devLCmd   uint64 = 0x00000008
	devRCmd   uint64 = 0x00000010
	devLAlt   uint64 = 0x00000020
	devRAlt   uint64 = 0x00000040
	devRCtl   uint64 = 0x00002000
)

// modFlags is indexed by HID usage - 0xE0 (LeftControl..RightGUI).
var modFlags = [8]uint64{
	flagControl | devLCtl, flagShift | devLShift, flagAlternate | devLAlt, flagCommand | devLCmd,
	flagControl | devRCtl, flagShift | devRShift, flagAlternate | devRAlt, flagCommand | devRCmd,
}

// modTracker tracks which HID modifier keys (0xE0-0xE7) are held on the host
// as a result of injected key events.
type modTracker struct{ held uint8 }

// isModifier reports whether hid is one of the eight HID modifier usages.
func isModifier(hid uint16) bool { return hid >= 0xE0 && hid <= 0xE7 }

// update records a key event and reports whether hid is a modifier key.
func (m *modTracker) update(hid uint16, down bool) bool {
	if !isModifier(hid) {
		return false
	}
	bit := uint8(1) << (hid - 0xE0)
	if down {
		m.held |= bit
	} else {
		m.held &^= bit
	}
	return true
}

// flags returns the CGEventFlags for the tracked modifiers merged with the
// generic modifier bits from an event's Mods (input.ModShift etc.).
func (m modTracker) flags(mods uint8) uint64 {
	var f uint64
	for i := range 8 {
		if m.held&(1<<i) != 0 {
			f |= modFlags[i]
		}
	}
	if mods&0x1 != 0 {
		f |= flagShift
	}
	if mods&0x2 != 0 {
		f |= flagControl
	}
	if mods&0x4 != 0 {
		f |= flagAlternate
	}
	if mods&0x8 != 0 {
		f |= flagCommand
	}
	return f
}

// CGEventType values.
const (
	evLeftDown     = 1
	evLeftUp       = 2
	evRightDown    = 3
	evRightUp      = 4
	evMouseMoved   = 5
	evLeftDragged  = 6
	evRightDragged = 7
	evKeyDown      = 10
	evKeyUp        = 11
	evFlagsChanged = 12
	evOtherDown    = 25
	evOtherUp      = 26
	evOtherDragged = 27
)

// buttonEventType returns the CGEventType for a press/release of button
// (0 left, 1 right, 2+ other).
func buttonEventType(button uint8, down bool) int {
	switch {
	case button == 0 && down:
		return evLeftDown
	case button == 0:
		return evLeftUp
	case button == 1 && down:
		return evRightDown
	case button == 1:
		return evRightUp
	case down:
		return evOtherDown
	default:
		return evOtherUp
	}
}

// heldButtons is a bit set of mouse buttons currently held down on the host.
type heldButtons uint32

func (h *heldButtons) set(button uint8, down bool) {
	if button >= 32 {
		return
	}
	if down {
		*h |= 1 << button
	} else {
		*h &^= 1 << button
	}
}

// moveType returns the CGEventType for a pointer move given the held buttons:
// a drag of the left, then right, then any other button, else a plain move.
func (h heldButtons) moveType() int {
	switch {
	case h&1 != 0:
		return evLeftDragged
	case h&2 != 0:
		return evRightDragged
	case h != 0:
		return evOtherDragged
	}
	return evMouseMoved
}

// clickMaxDist is how far (points) the pointer may move between presses and
// still count as a multi-click.
const clickMaxDist = 5

// clickTracker computes kCGMouseEventClickState for button presses.
type clickTracker struct {
	last   time.Time
	x, y   float64
	button uint8
	count  int64 // click state of the last press (also used for its release)
}

// down records a press of button at (x, y) and returns its click count: 1 for
// a single click, 2 for a double click, and so on.
func (c *clickTracker) down(button uint8, x, y float64, now time.Time, interval time.Duration) int64 {
	dx, dy := x-c.x, y-c.y
	if c.count > 0 && button == c.button && now.Sub(c.last) <= interval &&
		dx*dx+dy*dy <= clickMaxDist*clickMaxDist {
		c.count++
	} else {
		c.count = 1
	}
	c.last, c.x, c.y, c.button = now, x, y, button
	return c.count
}

// scrollAcc accumulates fractional pixel scroll deltas so that slow
// trackpad-style scrolling is not lost when converting to integer deltas.
type scrollAcc struct{ rx, ry float64 }

// add returns the integer deltas to post now, keeping the remainder.
func (s *scrollAcc) add(dx, dy float32) (ix, iy int32) {
	s.rx += float64(dx)
	s.ry += float64(dy)
	ix, iy = int32(s.rx), int32(s.ry) // truncates toward zero
	s.rx -= float64(ix)
	s.ry -= float64(iy)
	return ix, iy
}

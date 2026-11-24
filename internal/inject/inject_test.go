package inject

import (
	"testing"
	"time"

	"github.com/tangoui/kkdesk/internal/input"
)

func TestToGlobal(t *testing.T) {
	main := Rect{X: 0, Y: 0, W: 1440, H: 900}
	off := Rect{X: -1920, Y: -200, W: 1920, H: 1080}
	cases := []struct {
		nx, ny float32
		b      Rect
		x, y   float64
	}{
		{0, 0, main, 0, 0},
		{0.5, 0.5, main, 720, 450},
		{0.25, 0.75, main, 360, 675},
		{1, 1, main, 1439, 899}, // stays on the display
		{-0.5, 2, main, 0, 899}, // clamped
		{0.5, 0.5, off, -960, 340},
		{0, 0, off, -1920, -200},
		{0.5, 0.5, Rect{}, 0, 0},
	}
	for _, c := range cases {
		x, y := toGlobal(c.nx, c.ny, c.b)
		if x != c.x || y != c.y {
			t.Errorf("toGlobal(%v,%v,%+v) = (%v,%v), want (%v,%v)", c.nx, c.ny, c.b, x, y, c.x, c.y)
		}
	}
}

func TestModTracker(t *testing.T) {
	var m modTracker
	if m.flags(0) != 0 {
		t.Fatalf("initial flags = %#x", m.flags(0))
	}
	if !m.update(0xE1, true) { // left shift
		t.Fatal("left shift not treated as modifier")
	}
	if got, want := m.flags(0), flagShift|devLShift; got != want {
		t.Errorf("lshift flags = %#x, want %#x", got, want)
	}
	m.update(0xE7, true) // right GUI
	if got, want := m.flags(0), flagShift|devLShift|flagCommand|devRCmd; got != want {
		t.Errorf("lshift+rcmd flags = %#x, want %#x", got, want)
	}
	m.update(0xE1, false)
	if got, want := m.flags(0), flagCommand|devRCmd; got != want {
		t.Errorf("after lshift up = %#x, want %#x", got, want)
	}
	m.update(0xE7, false)
	if m.flags(0) != 0 {
		t.Errorf("all released flags = %#x", m.flags(0))
	}
	if m.update(0x04, true) { // 'a'
		t.Error("'a' treated as modifier")
	}
	if m.flags(0) != 0 {
		t.Errorf("'a' changed flags: %#x", m.flags(0))
	}
	// Every modifier usage maps to its generic and device bit.
	all := map[uint16]uint64{
		0xE0: flagControl | devLCtl, 0xE1: flagShift | devLShift,
		0xE2: flagAlternate | devLAlt, 0xE3: flagCommand | devLCmd,
		0xE4: flagControl | devRCtl, 0xE5: flagShift | devRShift,
		0xE6: flagAlternate | devRAlt, 0xE7: flagCommand | devRCmd,
	}
	for hid, want := range all {
		var mt modTracker
		mt.update(hid, true)
		if got := mt.flags(0); got != want {
			t.Errorf("hid %#x flags = %#x, want %#x", hid, got, want)
		}
	}
	// Event mods are merged in (sender-side state).
	if got, want := m.flags(input.ModShift|input.ModCtrl|input.ModAlt|input.ModMeta),
		flagShift|flagControl|flagAlternate|flagCommand; got != want {
		t.Errorf("mods-only flags = %#x, want %#x", got, want)
	}
	m.update(0xE0, true)
	if got, want := m.flags(input.ModCtrl), flagControl|devLCtl; got != want {
		t.Errorf("tracked+mods flags = %#x, want %#x", got, want)
	}
}

func TestClickTracker(t *testing.T) {
	var c clickTracker
	t0 := time.Unix(100, 0)
	iv := 500 * time.Millisecond
	if n := c.down(0, 10, 10, t0, iv); n != 1 {
		t.Fatalf("first click = %d", n)
	}
	if n := c.down(0, 11, 12, t0.Add(200*time.Millisecond), iv); n != 2 {
		t.Fatalf("double click = %d", n)
	}
	if n := c.down(0, 11, 12, t0.Add(400*time.Millisecond), iv); n != 3 {
		t.Fatalf("triple click = %d", n)
	}
	if c.count != 3 {
		t.Fatalf("up click state = %d", c.count)
	}
	// Too slow.
	if n := c.down(0, 11, 12, t0.Add(1000*time.Millisecond), iv); n != 1 {
		t.Fatalf("slow click = %d", n)
	}
	// Too far.
	if n := c.down(0, 40, 12, t0.Add(1100*time.Millisecond), iv); n != 1 {
		t.Fatalf("far click = %d", n)
	}
	// Different button.
	if n := c.down(1, 40, 12, t0.Add(1200*time.Millisecond), iv); n != 1 {
		t.Fatalf("other button click = %d", n)
	}
}

func TestEventTypes(t *testing.T) {
	cases := []struct {
		b    uint8
		down bool
		want int
	}{
		{0, true, evLeftDown}, {0, false, evLeftUp},
		{1, true, evRightDown}, {1, false, evRightUp},
		{2, true, evOtherDown}, {2, false, evOtherUp},
		{4, true, evOtherDown},
	}
	for _, c := range cases {
		if got := buttonEventType(c.b, c.down); got != c.want {
			t.Errorf("buttonEventType(%d,%v) = %d, want %d", c.b, c.down, got, c.want)
		}
	}
	var h heldButtons
	if got := h.moveType(); got != evMouseMoved {
		t.Errorf("no buttons: %d", got)
	}
	h.set(1, true)
	if got := h.moveType(); got != evRightDragged {
		t.Errorf("right held: %d", got)
	}
	h.set(0, true)
	if got := h.moveType(); got != evLeftDragged {
		t.Errorf("left+right held: %d", got)
	}
	h.set(0, false)
	h.set(1, false)
	h.set(2, true)
	if got := h.moveType(); got != evOtherDragged {
		t.Errorf("middle held: %d", got)
	}
	h.set(2, false)
	if got := h.moveType(); got != evMouseMoved {
		t.Errorf("released: %d", got)
	}
}

func TestScrollAccumulator(t *testing.T) {
	var s scrollAcc
	dx, dy := s.add(0.4, 1.5)
	if dx != 0 || dy != 1 {
		t.Fatalf("first = %d,%d", dx, dy)
	}
	dx, dy = s.add(0.7, 1.5)
	if dx != 1 || dy != 2 {
		t.Fatalf("second = %d,%d", dx, dy)
	}
	dx, dy = s.add(-3.2, -0.2)
	if dx != -3 || dy != 0 {
		t.Fatalf("third = %d,%d", dx, dy)
	}
}

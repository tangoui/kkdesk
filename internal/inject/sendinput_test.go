package inject

import (
	"testing"
	"unsafe"

	"github.com/tangoui/kkdesk/internal/input"
)

// The Win32 INPUT layout on 64-bit Windows: DWORD type, 4 bytes padding, then
// a union whose largest member (MOUSEINPUT) is 32 bytes; sizeof(INPUT) = 40.
func TestWinInputLayout(t *testing.T) {
	if unsafe.Sizeof(uintptr(0)) != 8 {
		t.Skip("layout checked for 64-bit targets only")
	}
	var in winInput
	if got := unsafe.Sizeof(in); got != 40 {
		t.Errorf("sizeof(INPUT) = %d, want 40", got)
	}
	if got := unsafe.Offsetof(in.u); got != 8 {
		t.Errorf("offsetof(INPUT.union) = %d, want 8", got)
	}
	if got := unsafe.Sizeof(in.u); got != 32 {
		t.Errorf("sizeof(INPUT.union) = %d, want 32", got)
	}

	var mi mouseInput
	if got := unsafe.Sizeof(mi); got != 32 {
		t.Errorf("sizeof(MOUSEINPUT) = %d, want 32", got)
	}
	for _, c := range []struct {
		name      string
		got, want uintptr
	}{
		{"dx", unsafe.Offsetof(mi.dx), 0},
		{"dy", unsafe.Offsetof(mi.dy), 4},
		{"mouseData", unsafe.Offsetof(mi.mouseData), 8},
		{"dwFlags", unsafe.Offsetof(mi.flags), 12},
		{"time", unsafe.Offsetof(mi.time), 16},
		{"dwExtraInfo", unsafe.Offsetof(mi.extraInfo), 24},
	} {
		if c.got != c.want {
			t.Errorf("offsetof(MOUSEINPUT.%s) = %d, want %d", c.name, c.got, c.want)
		}
	}

	var ki keybdInput
	if got := unsafe.Sizeof(ki); got != 24 {
		t.Errorf("sizeof(KEYBDINPUT) = %d, want 24", got)
	}
	for _, c := range []struct {
		name      string
		got, want uintptr
	}{
		{"wVk", unsafe.Offsetof(ki.vk), 0},
		{"wScan", unsafe.Offsetof(ki.scan), 2},
		{"dwFlags", unsafe.Offsetof(ki.flags), 4},
		{"time", unsafe.Offsetof(ki.time), 8},
		{"dwExtraInfo", unsafe.Offsetof(ki.extraInfo), 16},
	} {
		if c.got != c.want {
			t.Errorf("offsetof(KEYBDINPUT.%s) = %d, want %d", c.name, c.got, c.want)
		}
	}
}

// The union bytes are what SendInput reads; check them directly.
func TestWinInputBytes(t *testing.T) {
	if unsafe.Sizeof(uintptr(0)) != 8 {
		t.Skip("layout checked for 64-bit targets only")
	}
	m := mouseIn(mouseInput{dx: 0x01020304, dy: 0x05060708, mouseData: 0x090A0B0C, flags: 0x8001})
	b := (*[40]byte)(unsafe.Pointer(&m))
	if b[0] != inputMouse || b[1] != 0 || b[2] != 0 || b[3] != 0 {
		t.Errorf("mouse type bytes = % x", b[0:4])
	}
	want := []byte{0x04, 0x03, 0x02, 0x01, 0x08, 0x07, 0x06, 0x05, 0x0C, 0x0B, 0x0A, 0x09, 0x01, 0x80, 0x00, 0x00}
	if string(b[8:24]) != string(want) {
		t.Errorf("mouse union bytes = % x, want % x (little-endian)", b[8:24], want)
	}
	if m.mouse().dx != 0x01020304 || m.mouse().flags != 0x8001 {
		t.Errorf("mouse() = %+v", *m.mouse())
	}

	k := keybdIn(keybdInput{vk: 0x13, scan: 0xE045, flags: keyeventfScancode | keyeventfKeyUp})
	b = (*[40]byte)(unsafe.Pointer(&k))
	if b[0] != inputKeyboard {
		t.Errorf("keyboard type byte = %d", b[0])
	}
	want = []byte{0x13, 0x00, 0x45, 0xE0, 0x0A, 0x00, 0x00, 0x00}
	if string(b[8:16]) != string(want) {
		t.Errorf("keyboard union bytes = % x, want % x", b[8:16], want)
	}
}

func TestToAbsolute(t *testing.T) {
	// The inverse Windows applies: pixel = abs * size / 65536.
	pixel := func(abs, size int32) int32 { return int32(int64(abs) * int64(size) / 65536) }
	cases := []struct {
		n    float32
		size int32
		px   int32 // pixel it must land on
	}{
		{0, 1920, 0},
		{0.5, 1920, 960},
		{1, 1920, 1919},
		{-1, 1920, 0},
		{2, 1920, 1919},
		{0.25, 2560, 640},
		{0.999, 3840, 3836},
		{0.5, 1, 0},
	}
	for _, c := range cases {
		abs := toAbsolute(c.n, c.size)
		if abs < 0 || abs > 65535 {
			t.Errorf("toAbsolute(%v, %d) = %d, out of 0..65535", c.n, c.size, abs)
		}
		if got := pixel(abs, c.size); got != c.px {
			t.Errorf("toAbsolute(%v, %d) = %d -> pixel %d, want %d", c.n, c.size, abs, got, c.px)
		}
	}
	// Every pixel of common widths is reachable and hit at its centre.
	for _, size := range []int32{1280, 1366, 1920, 2560, 3840} {
		for p := int32(0); p < size; p++ {
			n := (float32(p) + 0.5) / float32(size)
			if got := pixel(toAbsolute(n, size), size); got != p {
				t.Fatalf("size %d: pixel %d maps to %d", size, p, got)
			}
		}
	}
	if got := toAbsolute(0.5, 0); got != 0 {
		t.Errorf("toAbsolute(0.5, 0) = %d, want 0", got)
	}
}

func TestWheelAcc(t *testing.T) {
	var w wheelAcc
	// One 40 px step is one notch; wire DY>0 scrolls up = Windows positive.
	if h, v := w.add(0, 40, 40); h != 0 || v != 120 {
		t.Errorf("add(0,40) = %d,%d; want 0,120", h, v)
	}
	// Wire DX>0 scrolls left; HWHEEL positive scrolls right, so it is negated.
	if h, v := w.add(40, 0, 40); h != -120 || v != 0 {
		t.Errorf("add(40,0) = %d,%d; want -120,0", h, v)
	}
	// Small trackpad deltas accumulate and nothing is lost.
	w = wheelAcc{}
	var sum int32
	for range 100 {
		_, v := w.add(0, -0.1, 40) // -0.3 units each
		sum += v
	}
	if sum != -30 {
		t.Errorf("100 x -0.1 px = %d units, want -30", sum)
	}
	// A different scale.
	w = wheelAcc{}
	if _, v := w.add(0, 10, 20); v != 60 {
		t.Errorf("add(0,10) at 20 px/notch = %d, want 60", v)
	}
	// A zero or negative scale falls back to the default.
	w = wheelAcc{}
	if _, v := w.add(0, 40, 0); v != 120 {
		t.Errorf("add(0,40) at scale 0 = %d, want 120", v)
	}
}

func TestWinTranslateMouse(t *testing.T) {
	tr := winTranslator{w: 1920, h: 1080, pxPerNotch: 40}
	abs := uint32(mouseeventfMove | mouseeventfAbsolute)

	ins, err := tr.inputs(input.Event{Kind: input.Move, X: 0.5, Y: 0.5})
	if err != nil || len(ins) != 1 {
		t.Fatalf("move: %v, %d inputs", err, len(ins))
	}
	m := ins[0].mouse()
	if ins[0].typ != inputMouse || m.flags != abs || m.dx != toAbsolute(0.5, 1920) || m.dy != toAbsolute(0.5, 1080) {
		t.Errorf("move = type %d %+v", ins[0].typ, *m)
	}

	buttons := []struct {
		button   uint8
		down, up uint32
		data     uint32
	}{
		{0, mouseeventfLeftDown, mouseeventfLeftUp, 0},
		{1, mouseeventfRightDown, mouseeventfRightUp, 0},
		{2, mouseeventfMiddleDown, mouseeventfMiddleUp, 0},
		{3, mouseeventfXDown, mouseeventfXUp, xbutton1},
		{4, mouseeventfXDown, mouseeventfXUp, xbutton2},
	}
	for _, b := range buttons {
		for _, down := range []bool{true, false} {
			ins, err := tr.inputs(input.Event{Kind: input.Button, X: 0.25, Y: 1, Button: b.button, Down: down})
			if err != nil || len(ins) != 1 {
				t.Fatalf("button %d: %v, %d inputs", b.button, err, len(ins))
			}
			want := b.up
			if down {
				want = b.down
			}
			m := ins[0].mouse()
			if m.flags != abs|want || m.mouseData != b.data || m.dx != toAbsolute(0.25, 1920) || m.dy != toAbsolute(1, 1080) {
				t.Errorf("button %d down=%v = %+v; want flags %#x data %d", b.button, down, *m, abs|want, b.data)
			}
		}
	}
	if _, err := tr.inputs(input.Event{Kind: input.Button, Button: 5, Down: true}); err == nil {
		t.Error("button 5: want error")
	}

	ins, err = tr.inputs(input.Event{Kind: input.Wheel, DX: -20, DY: 80})
	if err != nil || len(ins) != 2 {
		t.Fatalf("wheel: %v, %d inputs", err, len(ins))
	}
	if m := ins[0].mouse(); m.flags != mouseeventfWheel || int32(m.mouseData) != 240 {
		t.Errorf("vertical wheel = %+v", *m)
	}
	if m := ins[1].mouse(); m.flags != mouseeventfHWheel || int32(m.mouseData) != 60 {
		t.Errorf("horizontal wheel = %+v", *m)
	}
	ins, err = tr.inputs(input.Event{Kind: input.Wheel, DY: -0.001})
	if err != nil || len(ins) != 0 {
		t.Errorf("tiny wheel: %v, %d inputs; want none", err, len(ins))
	}
	ins, _ = tr.inputs(input.Event{Kind: input.Wheel, DY: -40})
	if len(ins) != 1 || int32(ins[0].mouse().mouseData) != -120 {
		t.Errorf("wheel down = %d inputs", len(ins))
	}
}

func TestWinTranslateKeys(t *testing.T) {
	var tr winTranslator
	cases := []struct {
		name  string
		hid   uint16
		down  bool
		vk    uint16
		scan  uint16
		flags uint32
	}{
		{"A down", 0x04, true, 0, 0x1E, keyeventfScancode},
		{"A up", 0x04, false, 0, 0x1E, keyeventfScancode | keyeventfKeyUp},
		{"RightCtrl down", 0xE4, true, 0, 0x1D, keyeventfScancode | keyeventfExtendedKey},
		{"Up up", 0x52, false, 0, 0x48, keyeventfScancode | keyeventfExtendedKey | keyeventfKeyUp},
		{"Pause down", 0x48, true, 0x13, 0, 0},
		{"Pause up", 0x48, false, 0x13, 0, keyeventfKeyUp},
	}
	for _, c := range cases {
		ins, err := tr.inputs(input.Event{Kind: input.Key, HID: c.hid, Down: c.down, Mods: input.ModShift})
		if err != nil || len(ins) != 1 {
			t.Fatalf("%s: %v, %d inputs", c.name, err, len(ins))
		}
		k := ins[0].keybd()
		if ins[0].typ != inputKeyboard || k.vk != c.vk || k.scan != c.scan || k.flags != c.flags {
			t.Errorf("%s = type %d %+v; want vk %#x scan %#x flags %#x", c.name, ins[0].typ, *k, c.vk, c.scan, c.flags)
		}
	}
	if _, err := tr.inputs(input.Event{Kind: input.Key, HID: 0x01, Down: true}); err == nil {
		t.Error("unmapped HID: want error")
	}
	if _, err := tr.inputs(input.Event{Kind: 99}); err == nil {
		t.Error("unknown kind: want error")
	}
}

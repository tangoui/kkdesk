package inject

import (
	"fmt"
	"math"
	"unsafe"

	"mmdesk/internal/input"
)

// This file holds the platform-independent half of the Windows backend: the
// Win32 INPUT structures and the translation of input.Events into them. It
// has no build tag so its layout and logic are tested on every platform;
// inject_windows.go only hands the result to SendInput.

// WheelPixelsPerNotch is how many logical pixels of wheel delta make one
// WHEEL_DELTA (120, one mouse wheel notch) on a Windows host. The Mac viewer
// reports one line of a notched mouse wheel as 40 px, so the default turns
// one notch on the viewer into one notch on the host. Windows reads it in New;
// other platforms ignore it.
var WheelPixelsPerNotch = 40.0

// Win32 constants (winuser.h).
const (
	inputMouse    = 0 // INPUT_MOUSE
	inputKeyboard = 1 // INPUT_KEYBOARD

	mouseeventfMove       = 0x0001
	mouseeventfLeftDown   = 0x0002
	mouseeventfLeftUp     = 0x0004
	mouseeventfRightDown  = 0x0008
	mouseeventfRightUp    = 0x0010
	mouseeventfMiddleDown = 0x0020
	mouseeventfMiddleUp   = 0x0040
	mouseeventfXDown      = 0x0080
	mouseeventfXUp        = 0x0100
	mouseeventfWheel      = 0x0800
	mouseeventfHWheel     = 0x1000
	mouseeventfAbsolute   = 0x8000

	xbutton1 = 0x0001
	xbutton2 = 0x0002

	keyeventfExtendedKey = 0x0001
	keyeventfKeyUp       = 0x0002
	keyeventfScancode    = 0x0008

	wheelDelta = 120
)

// mouseInput is MOUSEINPUT.
type mouseInput struct {
	dx, dy    int32
	mouseData uint32
	flags     uint32
	time      uint32
	extraInfo uintptr
}

// keybdInput is KEYBDINPUT.
type keybdInput struct {
	vk        uint16
	scan      uint16
	flags     uint32
	time      uint32
	extraInfo uintptr
}

// winInput is INPUT on 64-bit Windows: the type, padding, and a 32-byte,
// 8-byte-aligned union of MOUSEINPUT / KEYBDINPUT / HARDWAREINPUT.
type winInput struct {
	typ uint32
	_   uint32
	u   [4]uint64
}

func mouseIn(mi mouseInput) winInput {
	in := winInput{typ: inputMouse}
	*in.mouse() = mi
	return in
}

func keybdIn(ki keybdInput) winInput {
	in := winInput{typ: inputKeyboard}
	*in.keybd() = ki
	return in
}

func (in *winInput) mouse() *mouseInput { return (*mouseInput)(unsafe.Pointer(&in.u)) }
func (in *winInput) keybd() *keybdInput { return (*keybdInput)(unsafe.Pointer(&in.u)) }

// toAbsolute maps a normalized coordinate (clamped to [0,1]) on a screen
// axis of size pixels to a MOUSEEVENTF_ABSOLUTE coordinate (0..65535). It
// aims at the centre of the target pixel so that Windows' conversion back
// (pixel = abs*size/65536) lands on it whatever its rounding.
func toAbsolute(n float32, size int32) int32 {
	if size <= 0 {
		return 0
	}
	f := max(0, min(1, float64(n)))
	p := min(int64(f*float64(size)), int64(size)-1)
	abs := ((2*p + 1) * 65536) / (2 * int64(size))
	return int32(min(abs, 65535))
}

// wheelAcc converts wire wheel deltas (logical pixels, CGScrollWheel sign
// convention: DY > 0 scrolls up, DX > 0 scrolls left) to WHEEL_DELTA units,
// keeping the fractional remainder so slow trackpad scrolling is not lost.
type wheelAcc struct{ rh, rv float64 }

// add returns the MOUSEEVENTF_HWHEEL (h, > 0 scrolls right) and
// MOUSEEVENTF_WHEEL (v, > 0 scrolls up) amounts to send now. pxPerNotch <= 0
// means the default of 40.
func (w *wheelAcc) add(dx, dy float32, pxPerNotch float64) (h, v int32) {
	if pxPerNotch <= 0 {
		pxPerNotch = 40
	}
	scale := wheelDelta / pxPerNotch
	w.rh -= float64(dx) * scale
	w.rv += float64(dy) * scale
	h, v = int32(math.Round(w.rh)), int32(math.Round(w.rv))
	w.rh -= float64(h)
	w.rv -= float64(v)
	return h, v
}

// winTranslator turns input.Events into SendInput records for a primary
// screen of w x h pixels.
type winTranslator struct {
	w, h       int32
	pxPerNotch float64
	wheel      wheelAcc
}

// inputs returns the INPUT records for e:
//   - Move / Button: MOUSEEVENTF_MOVE|MOUSEEVENTF_ABSOLUTE to (X, Y) on the
//     primary monitor (without MOUSEEVENTF_VIRTUALDESK, 0..65535 spans the
//     primary monitor), plus the button flag (0 left, 1 right, 2 middle,
//     3/4 X buttons). Windows derives drags and double clicks itself.
//   - Key: the scan code from input.WinScanCode (KEYEVENTF_SCANCODE, plus
//     KEYEVENTF_EXTENDEDKEY for E0 keys), or the virtual key from
//     input.WinVK for keys without one (Pause).
//   - Wheel: MOUSEEVENTF_WHEEL and/or MOUSEEVENTF_HWHEEL; none if the
//     accumulated delta is still below half a unit.
//
// Mods is ignored: modifier state on Windows is the real key state, which
// the viewer keeps in sync with its own Key events.
func (t *winTranslator) inputs(e input.Event) ([]winInput, error) {
	switch e.Kind {
	case input.Move:
		return []winInput{mouseIn(t.pointer(e, 0, 0))}, nil
	case input.Button:
		var down, up, data uint32
		switch e.Button {
		case 0:
			down, up = mouseeventfLeftDown, mouseeventfLeftUp
		case 1:
			down, up = mouseeventfRightDown, mouseeventfRightUp
		case 2:
			down, up = mouseeventfMiddleDown, mouseeventfMiddleUp
		case 3:
			down, up, data = mouseeventfXDown, mouseeventfXUp, xbutton1
		case 4:
			down, up, data = mouseeventfXDown, mouseeventfXUp, xbutton2
		default:
			return nil, fmt.Errorf("inject: mouse button %d not supported on Windows", e.Button)
		}
		flag := up
		if e.Down {
			flag = down
		}
		return []winInput{mouseIn(t.pointer(e, flag, data))}, nil
	case input.Key:
		var ki keybdInput
		if scan, ext, ok := input.WinScanCode(e.HID); ok {
			ki.scan, ki.flags = scan, keyeventfScancode
			if ext {
				ki.flags |= keyeventfExtendedKey
			}
		} else if vk, ok := input.WinVK(e.HID); ok {
			ki.vk = vk
		} else {
			return nil, fmt.Errorf("inject: no Windows scan code for HID usage %#04x", e.HID)
		}
		if !e.Down {
			ki.flags |= keyeventfKeyUp
		}
		return []winInput{keybdIn(ki)}, nil
	case input.Wheel:
		h, v := t.wheel.add(e.DX, e.DY, t.pxPerNotch)
		var ins []winInput
		if v != 0 {
			ins = append(ins, mouseIn(mouseInput{mouseData: uint32(v), flags: mouseeventfWheel}))
		}
		if h != 0 {
			ins = append(ins, mouseIn(mouseInput{mouseData: uint32(h), flags: mouseeventfHWheel}))
		}
		return ins, nil
	}
	return nil, fmt.Errorf("inject: unknown event kind %d", e.Kind)
}

// pointer is an absolute move to e's position with extra button flags.
func (t *winTranslator) pointer(e input.Event, flags, data uint32) mouseInput {
	return mouseInput{
		dx:        toAbsolute(e.X, t.w),
		dy:        toAbsolute(e.Y, t.h),
		mouseData: data,
		flags:     mouseeventfMove | mouseeventfAbsolute | flags,
	}
}

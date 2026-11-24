package input

import "testing"

// tangoWinScanHID and tangoWinExtScanHID are copies of Tango's
// internal/platform/windows winScanHID and winExtScanHID tables (Set 1 scan
// code without / with the extended E0 flag -> USB HID usage, page 0x07). They
// are test data only: WinScanCode must invert them.
var tangoWinScanHID = map[uint16]uint16{
	0x01: 0x29,                                                 // Escape
	0x02: 0x1E, 0x03: 0x1F, 0x04: 0x20, 0x05: 0x21, 0x06: 0x22, // 1-5
	0x07: 0x23, 0x08: 0x24, 0x09: 0x25, 0x0A: 0x26, 0x0B: 0x27, // 6-9 0
	0x0C: 0x2D, 0x0D: 0x2E, 0x0E: 0x2A, 0x0F: 0x2B, // - = Backspace Tab
	0x10: 0x14, 0x11: 0x1A, 0x12: 0x08, 0x13: 0x15, 0x14: 0x17, // Q W E R T
	0x15: 0x1C, 0x16: 0x18, 0x17: 0x0C, 0x18: 0x12, 0x19: 0x13, // Y U I O P
	0x1A: 0x2F, 0x1B: 0x30, 0x1C: 0x28, 0x1D: 0xE0, // [ ] Enter LeftCtrl
	0x1E: 0x04, 0x1F: 0x16, 0x20: 0x07, 0x21: 0x09, 0x22: 0x0A, // A S D F G
	0x23: 0x0B, 0x24: 0x0D, 0x25: 0x0E, 0x26: 0x0F, // H J K L
	0x27: 0x33, 0x28: 0x34, 0x29: 0x35, 0x2A: 0xE1, 0x2B: 0x31, // ; ' ` LeftShift \
	0x2C: 0x1D, 0x2D: 0x1B, 0x2E: 0x06, 0x2F: 0x19, 0x30: 0x05, // Z X C V B
	0x31: 0x11, 0x32: 0x10, 0x33: 0x36, 0x34: 0x37, 0x35: 0x38, // N M , . /
	0x36: 0xE5, 0x37: 0x55, 0x38: 0xE2, 0x39: 0x2C, 0x3A: 0x39, // RightShift KP* LeftAlt Space CapsLock
	0x3B: 0x3A, 0x3C: 0x3B, 0x3D: 0x3C, 0x3E: 0x3D, 0x3F: 0x3E, // F1-F5
	0x40: 0x3F, 0x41: 0x40, 0x42: 0x41, 0x43: 0x42, 0x44: 0x43, // F6-F10
	0x45: 0x48,                                     // Pause (NumLock sends 0x45 with the extended flag)
	0x46: 0x47,                                     // ScrollLock
	0x47: 0x5F, 0x48: 0x60, 0x49: 0x61, 0x4A: 0x56, // KP7 KP8 KP9 KP-
	0x4B: 0x5C, 0x4C: 0x5D, 0x4D: 0x5E, 0x4E: 0x57, // KP4 KP5 KP6 KP+
	0x4F: 0x59, 0x50: 0x5A, 0x51: 0x5B, 0x52: 0x62, 0x53: 0x63, // KP1 KP2 KP3 KP0 KP.
	0x54: 0x46,             // SysRq (Alt+Print Screen)
	0x56: 0x64,             // the key next to left Shift on ISO keyboards
	0x57: 0x44, 0x58: 0x45, // F11 F12
	0x59: 0x67,                                                             // KP=
	0x64: 0x68, 0x65: 0x69, 0x66: 0x6A, 0x67: 0x6B, 0x68: 0x6C, 0x69: 0x6D, // F13-F18
	0x6A: 0x6E, 0x6B: 0x6F, 0x6C: 0x70, 0x6D: 0x71, 0x6E: 0x72, 0x76: 0x73, // F19-F24
	0x70: 0x88, // Kana (International2)
	0x71: 0x91, // Hanja (LANG2)
	0x72: 0x90, // Hangul (LANG1)
	0x73: 0x87, // Ro (International1)
	0x79: 0x8A, // Henkan (International4)
	0x7B: 0x8B, // Muhenkan (International5)
	0x7D: 0x89, // Yen (International3)
	0x7E: 0x85, // keypad comma (Brazilian)
	0xF1: 0x91, // Hanja on keyboards that send F1
	0xF2: 0x90, // Hangul on keyboards that send F2
}

var tangoWinExtScanHID = map[uint16]uint16{
	0x1C: 0x58,                         // keypad Enter
	0x1D: 0xE4,                         // RightCtrl
	0x20: 0x7F,                         // Mute
	0x2E: 0x81,                         // VolumeDown
	0x30: 0x80,                         // VolumeUp
	0x35: 0x54,                         // KP/
	0x37: 0x46,                         // Print Screen
	0x38: 0xE6,                         // RightAlt
	0x45: 0x53,                         // NumLock
	0x46: 0x48,                         // Ctrl+Pause (Break)
	0x47: 0x4A, 0x48: 0x52, 0x49: 0x4B, // Home Up PageUp
	0x4B: 0x50, 0x4D: 0x4F, // Left Right
	0x4F: 0x4D, 0x50: 0x51, 0x51: 0x4E, // End Down PageDown
	0x52: 0x49, 0x53: 0x4C, // Insert Delete
	0x5B: 0xE3, 0x5C: 0xE7, 0x5D: 0x65, // LeftWin RightWin Apps
	0x5E: 0x66, // Power
}

// winScanKey is a scan code with its extended flag, for test bookkeeping.
type winScanKey struct {
	scan uint16
	ext  bool
}

// tangoWinForward merges the two Tango tables into HID -> all scan codes
// that produce it.
func tangoWinForward() map[uint16][]winScanKey {
	fwd := make(map[uint16][]winScanKey)
	for scan, hid := range tangoWinScanHID {
		fwd[hid] = append(fwd[hid], winScanKey{scan, false})
	}
	for scan, hid := range tangoWinExtScanHID {
		fwd[hid] = append(fwd[hid], winScanKey{scan, true})
	}
	return fwd
}

func TestWinScanCodeInvertsTangoTables(t *testing.T) {
	fwd := tangoWinForward()
	for hid, keys := range fwd {
		scan, ext, ok := WinScanCode(hid)
		if len(keys) == 1 {
			if !ok || scan != keys[0].scan || ext != keys[0].ext {
				t.Errorf("WinScanCode(0x%02X) = 0x%02X, ext=%v, %v; want 0x%02X, ext=%v, true",
					hid, scan, ext, ok, keys[0].scan, keys[0].ext)
			}
			continue
		}
		// Duplicates: the chosen scan code (if any) must be one of Tango's.
		if !ok {
			if _, vk := WinVK(hid); !vk {
				t.Errorf("duplicate HID 0x%02X has neither a scan code nor a VK fallback", hid)
			}
			continue
		}
		found := false
		for _, k := range keys {
			if k.scan == scan && k.ext == ext {
				found = true
			}
		}
		if !found {
			t.Errorf("WinScanCode(0x%02X) = 0x%02X ext=%v is not in Tango's tables %v", hid, scan, ext, keys)
		}
	}
	// Every reverse entry comes from a Tango entry.
	for hid, k := range winScanCodes {
		tbl := tangoWinScanHID
		if k.ext {
			tbl = tangoWinExtScanHID
		}
		if got, ok := tbl[k.scan]; !ok || got != hid {
			t.Errorf("reverse entry HID 0x%02X -> 0x%02X ext=%v has no matching Tango entry", hid, k.scan, k.ext)
		}
	}
	// Every Tango HID is reachable by scan code or VK.
	for hid := range fwd {
		_, _, ok := WinScanCode(hid)
		_, vk := WinVK(hid)
		if !ok && !vk {
			t.Errorf("HID 0x%02X is not injectable", hid)
		}
	}
}

func TestWinScanCodeSpecificKeys(t *testing.T) {
	cases := []struct {
		name string
		hid  uint16
		scan uint16
		ext  bool
	}{
		{"A", 0x04, 0x1E, false},
		{"Enter", 0x28, 0x1C, false},
		{"KeypadEnter", 0x58, 0x1C, true},
		{"LeftControl", 0xE0, 0x1D, false},
		{"RightControl", 0xE4, 0x1D, true},
		{"LeftAlt", 0xE2, 0x38, false},
		{"RightAlt", 0xE6, 0x38, true},
		{"LeftGUI", 0xE3, 0x5B, true},
		{"RightGUI", 0xE7, 0x5C, true},
		{"Right", 0x4F, 0x4D, true},
		{"Left", 0x50, 0x4B, true},
		{"Down", 0x51, 0x50, true},
		{"Up", 0x52, 0x48, true},
		{"NumLock", 0x53, 0x45, true},
		{"PrintScreen", 0x46, 0x37, true},
		{"ScrollLock", 0x47, 0x46, false},
		{"ISOBackslash", 0x64, 0x56, false},
		{"F1", 0x3A, 0x3B, false},
		{"F12", 0x45, 0x58, false},
		{"F13", 0x68, 0x64, false},
		{"F24", 0x73, 0x76, false},
		{"JISYen", 0x89, 0x7D, false},
		{"Hangul", 0x90, 0x72, false},
		{"Hanja", 0x91, 0x71, false},
		{"KeypadDivide", 0x54, 0x35, true},
		{"KeypadMultiply", 0x55, 0x37, false},
	}
	for _, c := range cases {
		scan, ext, ok := WinScanCode(c.hid)
		if !ok || scan != c.scan || ext != c.ext {
			t.Errorf("%s: WinScanCode(0x%02X) = 0x%02X, ext=%v, %v; want 0x%02X, ext=%v, true",
				c.name, c.hid, scan, ext, ok, c.scan, c.ext)
		}
	}
}

func TestWinPauseUsesVK(t *testing.T) {
	if scan, ext, ok := WinScanCode(0x48); ok {
		t.Errorf("WinScanCode(Pause) = 0x%02X, ext=%v, true; want no scan code (E1 sequence)", scan, ext)
	}
	if vk, ok := WinVK(0x48); !ok || vk != 0x13 {
		t.Errorf("WinVK(Pause) = 0x%02X, %v; want VK_PAUSE 0x13, true", vk, ok)
	}
	if vk, ok := WinVK(0x04); ok {
		t.Errorf("WinVK(A) = 0x%02X, true; want false (A has a scan code)", vk)
	}
}

func TestWinScanCodeUnknown(t *testing.T) {
	for _, hid := range []uint16{0x00, 0x01, 0x32, 0xFF, 0x0104, 0xFFFF} {
		if scan, ext, ok := WinScanCode(hid); ok {
			t.Errorf("WinScanCode(0x%02X) = 0x%02X, ext=%v, true; want false", hid, scan, ext)
		}
		if vk, ok := WinVK(hid); ok {
			t.Errorf("WinVK(0x%02X) = 0x%02X, true; want false", hid, vk)
		}
	}
}

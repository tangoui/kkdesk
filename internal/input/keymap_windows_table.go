package input

// WinScanCode maps a USB HID keyboard usage (page 0x07) to a Set 1 scan code
// for SendInput with KEYEVENTF_SCANCODE; extended reports whether the key
// needs the E0 prefix (KEYEVENTF_EXTENDEDKEY). ok is false for usages with
// no injectable scan code; for some of those (Pause) WinVK gives a virtual
// key fallback.
//
// The table is the inverse of Tango's Windows scan code -> HID tables
// (winScanHID / winExtScanHID), so a key pressed on a Windows viewer arrives
// on a Windows host as the same physical key. Where Tango maps two scan
// codes to one HID usage, the one a real keyboard sends for the plain key
// press is used:
//
//   - Print Screen (0x46): E0 37, not 54 (SysRq, what Alt+Print Screen sends).
//   - Pause (0x48): no scan code. A real Pause sends E1 1D 45, which
//     KEYEVENTF_SCANCODE cannot express; 45 is Pause only as Windows reports
//     it, and E0 46 is Ctrl+Break. WinVK returns VK_PAUSE instead.
//   - Hanja (0x91) / Hangul (0x90): 71 / 72 (what Windows reports for the
//     Korean keys), not the make-only F1 / F2 some keyboards send.
func WinScanCode(hid uint16) (scan uint16, extended bool, ok bool) {
	k, ok := winScanCodes[hid]
	return k.scan, k.ext, ok
}

// WinVK returns a Windows virtual key code for HID usages that cannot be
// injected by scan code (WinScanCode reports ok=false), to be sent with
// KEYBDINPUT.wVk. ok is false for every other usage.
func WinVK(hid uint16) (vk uint16, ok bool) {
	vk, ok = winVKs[hid]
	return vk, ok
}

type winScan struct {
	scan uint16
	ext  bool
}

// winVKs: HID usage -> virtual key, for keys without a usable scan code.
var winVKs = map[uint16]uint16{
	0x48: 0x13, // Pause -> VK_PAUSE
}

// winScanCodes: HID usage -> Set 1 scan code (+ extended flag).
var winScanCodes = map[uint16]winScan{
	// Letters A-Z.
	0x04: {0x1E, false}, 0x05: {0x30, false}, 0x06: {0x2E, false}, 0x07: {0x20, false}, // A B C D
	0x08: {0x12, false}, 0x09: {0x21, false}, 0x0A: {0x22, false}, 0x0B: {0x23, false}, // E F G H
	0x0C: {0x17, false}, 0x0D: {0x24, false}, 0x0E: {0x25, false}, 0x0F: {0x26, false}, // I J K L
	0x10: {0x32, false}, 0x11: {0x31, false}, 0x12: {0x18, false}, 0x13: {0x19, false}, // M N O P
	0x14: {0x10, false}, 0x15: {0x13, false}, 0x16: {0x1F, false}, 0x17: {0x14, false}, // Q R S T
	0x18: {0x16, false}, 0x19: {0x2F, false}, 0x1A: {0x11, false}, 0x1B: {0x2D, false}, // U V W X
	0x1C: {0x15, false}, 0x1D: {0x2C, false}, // Y Z
	// Digits 1-9, 0.
	0x1E: {0x02, false}, 0x1F: {0x03, false}, 0x20: {0x04, false}, 0x21: {0x05, false}, 0x22: {0x06, false},
	0x23: {0x07, false}, 0x24: {0x08, false}, 0x25: {0x09, false}, 0x26: {0x0A, false}, 0x27: {0x0B, false},
	// Editing and punctuation.
	0x28: {0x1C, false}, // Enter
	0x29: {0x01, false}, // Escape
	0x2A: {0x0E, false}, // Backspace
	0x2B: {0x0F, false}, // Tab
	0x2C: {0x39, false}, // Space
	0x2D: {0x0C, false}, // Minus
	0x2E: {0x0D, false}, // Equal
	0x2F: {0x1A, false}, // LeftBracket
	0x30: {0x1B, false}, // RightBracket
	0x31: {0x2B, false}, // Backslash
	0x33: {0x27, false}, // Semicolon
	0x34: {0x28, false}, // Quote
	0x35: {0x29, false}, // Grave
	0x36: {0x33, false}, // Comma
	0x37: {0x34, false}, // Period
	0x38: {0x35, false}, // Slash
	0x39: {0x3A, false}, // CapsLock
	0x64: {0x56, false}, // Non-US Backslash (ISO key next to left Shift)
	// Function keys F1-F24.
	0x3A: {0x3B, false}, 0x3B: {0x3C, false}, 0x3C: {0x3D, false}, 0x3D: {0x3E, false}, // F1-F4
	0x3E: {0x3F, false}, 0x3F: {0x40, false}, 0x40: {0x41, false}, 0x41: {0x42, false}, // F5-F8
	0x42: {0x43, false}, 0x43: {0x44, false}, 0x44: {0x57, false}, 0x45: {0x58, false}, // F9-F12
	0x68: {0x64, false}, 0x69: {0x65, false}, 0x6A: {0x66, false}, 0x6B: {0x67, false}, // F13-F16
	0x6C: {0x68, false}, 0x6D: {0x69, false}, 0x6E: {0x6A, false}, 0x6F: {0x6B, false}, // F17-F20
	0x70: {0x6C, false}, 0x71: {0x6D, false}, 0x72: {0x6E, false}, 0x73: {0x76, false}, // F21-F24
	// Print Screen, Scroll Lock (Pause: see WinVK).
	0x46: {0x37, true},  // Print Screen (E0 37; 54 is SysRq)
	0x47: {0x46, false}, // Scroll Lock
	// Navigation.
	0x49: {0x52, true}, // Insert
	0x4A: {0x47, true}, // Home
	0x4B: {0x49, true}, // PageUp
	0x4C: {0x53, true}, // Delete
	0x4D: {0x4F, true}, // End
	0x4E: {0x51, true}, // PageDown
	0x4F: {0x4D, true}, // Right
	0x50: {0x4B, true}, // Left
	0x51: {0x50, true}, // Down
	0x52: {0x48, true}, // Up
	0x65: {0x5D, true}, // Application (context menu)
	0x66: {0x5E, true}, // Power
	// Keypad.
	0x53: {0x45, true},                                            // NumLock (E0 45 as Windows reports it; plain 45 is Pause)
	0x54: {0x35, true},                                            // KP /
	0x55: {0x37, false},                                           // KP *
	0x56: {0x4A, false},                                           // KP -
	0x57: {0x4E, false},                                           // KP +
	0x58: {0x1C, true},                                            // KP Enter
	0x59: {0x4F, false}, 0x5A: {0x50, false}, 0x5B: {0x51, false}, // KP1-3
	0x5C: {0x4B, false}, 0x5D: {0x4C, false}, 0x5E: {0x4D, false}, // KP4-6
	0x5F: {0x47, false}, 0x60: {0x48, false}, 0x61: {0x49, false}, // KP7-9
	0x62: {0x52, false}, // KP0
	0x63: {0x53, false}, // KP .
	0x67: {0x59, false}, // KP =
	0x85: {0x7E, false}, // KP comma (Brazilian)
	// Media keys.
	0x7F: {0x20, true}, // Mute
	0x80: {0x30, true}, // VolumeUp
	0x81: {0x2E, true}, // VolumeDown
	// International / IME keys.
	0x87: {0x73, false}, // Ro (International1)
	0x88: {0x70, false}, // Kana (International2)
	0x89: {0x7D, false}, // Yen (International3)
	0x8A: {0x79, false}, // Henkan (International4)
	0x8B: {0x7B, false}, // Muhenkan (International5)
	0x90: {0x72, false}, // Hangul (LANG1; some keyboards send make-only F2)
	0x91: {0x71, false}, // Hanja (LANG2; some keyboards send make-only F1)
	// Modifiers.
	0xE0: {0x1D, false}, // LeftControl
	0xE1: {0x2A, false}, // LeftShift
	0xE2: {0x38, false}, // LeftAlt
	0xE3: {0x5B, true},  // LeftGUI (Windows)
	0xE4: {0x1D, true},  // RightControl
	0xE5: {0x36, false}, // RightShift
	0xE6: {0x38, true},  // RightAlt
	0xE7: {0x5C, true},  // RightGUI (Windows)
}

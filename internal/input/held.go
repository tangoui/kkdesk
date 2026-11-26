package input

import (
	"slices"
	"sync"
)

// Held remembers which keys and mouse buttons are down after a stream of
// events, so whoever stops the stream mid-press (a session that ends, a
// controller switched to view-only) can release them: otherwise the other
// side keeps a drag or a modifier held. It is safe for concurrent use.
type Held struct {
	mu      sync.Mutex
	keys    []uint16 // HID usages, in press order
	buttons []uint8
	x, y    float32 // the last pointer position, for button releases
}

// Track records e (call it for each event that took effect).
func (h *Held) Track(e Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	switch e.Kind {
	case Move:
		h.x, h.y = e.X, e.Y
	case Button:
		h.x, h.y = e.X, e.Y
		h.buttons = slices.DeleteFunc(h.buttons, func(b uint8) bool { return b == e.Button })
		if e.Down {
			h.buttons = append(h.buttons, e.Button)
		}
	case Key:
		h.keys = slices.DeleteFunc(h.keys, func(k uint16) bool { return k == e.HID })
		if e.Down {
			h.keys = append(h.keys, e.HID)
		}
	}
}

// Release returns the events that let go of everything still down —
// buttons first, then keys in reverse press order (modifiers last) — and
// forgets them.
func (h *Held) Release() []Event {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []Event
	for _, b := range h.buttons {
		out = append(out, Event{Kind: Button, X: h.x, Y: h.y, Button: b})
	}
	for i := len(h.keys) - 1; i >= 0; i-- {
		out = append(out, Event{Kind: Key, HID: h.keys[i]})
	}
	h.buttons, h.keys = nil, nil
	return out
}

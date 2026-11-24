//go:build darwin && cgo

package inject

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework ApplicationServices -framework CoreGraphics -framework AppKit -framework Foundation
#include "inject_darwin.h"
*/
import "C"

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/tangoui/kkdesk/internal/input"
)

// HasPermission reports whether the process is trusted for Accessibility
// (AXIsProcessTrusted), which CGEventPost needs. It never shows a prompt.
func HasPermission() bool { return C.mminj_has_permission() != 0 }

// RequestPermission shows the system Accessibility prompt
// (AXIsProcessTrustedWithOptions with prompt=true) if the process is not
// trusted yet. The user grants it in System Settings > Privacy & Security >
// Accessibility; the process may need a restart afterwards.
func RequestPermission() { C.mminj_request_permission() }

// Injector posts input.Events to the local session with CGEventPost
// (kCGHIDEventTap). Normalized coordinates refer to the main display. It is
// safe for concurrent use; events are posted in Apply call order.
type Injector struct {
	mu       sync.Mutex
	bounds   Rect
	dblClick time.Duration
	mods     modTracker
	held     heldButtons
	clicks   clickTracker
	scroll   scrollAcc
	x, y     float64 // last pointer position (global points)
}

// New returns an Injector for the main display. It does not check the
// Accessibility permission; without it events are silently dropped by the
// system, so check HasPermission first.
func New() (*Injector, error) {
	in := &Injector{dblClick: time.Duration(float64(C.mminj_double_click_interval()) * float64(time.Second))}
	if in.dblClick <= 0 {
		in.dblClick = 500 * time.Millisecond
	}
	in.Refresh()
	if in.bounds.W <= 0 || in.bounds.H <= 0 {
		return nil, errors.New("inject: main display has no bounds")
	}
	return in, nil
}

// Refresh re-reads the main display bounds (CGDisplayBounds, points). Apply
// calls it for every pointer event, so it only needs calling directly to read
// Bounds after a display change.
func (in *Injector) Refresh() {
	in.mu.Lock()
	in.refresh()
	in.mu.Unlock()
}

func (in *Injector) refresh() {
	var x, y, w, h C.double
	C.mminj_main_bounds(&x, &y, &w, &h)
	in.bounds = Rect{X: float64(x), Y: float64(y), W: float64(w), H: float64(h)}
}

// Bounds returns the cached main display bounds in global points.
func (in *Injector) Bounds() Rect {
	in.mu.Lock()
	defer in.mu.Unlock()
	return in.bounds
}

// Apply posts one event:
//   - Move: mouse moved, or dragged while a button is held.
//   - Button: press/release (0 left, 1 right, 2+ other) at (X, Y) with
//     click state for double/triple clicks.
//   - Key: HID usage mapped with input.MacKeyCode; modifier keys (0xE0-0xE7)
//     post kCGEventFlagsChanged and update the tracked modifier flags.
//   - Wheel: pixel scroll (DY vertical, DX horizontal; CGScrollWheel sign
//     convention, fractions accumulated).
//
// Unknown kinds and unmapped HID usages return an error and post nothing.
func (in *Injector) Apply(e input.Event) error {
	in.mu.Lock()
	defer in.mu.Unlock()
	switch e.Kind {
	case input.Move:
		in.refresh()
		in.x, in.y = toGlobal(e.X, e.Y, in.bounds)
		btn := 0
		t := in.held.moveType()
		switch t {
		case evRightDragged:
			btn = 1
		case evOtherDragged:
			for i := 2; i < 32; i++ {
				if in.held&(1<<i) != 0 {
					btn = i
					break
				}
			}
		}
		return check(C.mminj_mouse(C.int(t), C.double(in.x), C.double(in.y), C.int(btn), 0, C.uint64_t(in.mods.flags(0))))
	case input.Button:
		in.refresh()
		in.x, in.y = toGlobal(e.X, e.Y, in.bounds)
		var cs int64
		if e.Down {
			cs = in.clicks.down(e.Button, in.x, in.y, time.Now(), in.dblClick)
		} else {
			cs = 1
			if e.Button == in.clicks.button && in.clicks.count > 0 {
				cs = in.clicks.count
			}
		}
		in.held.set(e.Button, e.Down)
		t := buttonEventType(e.Button, e.Down)
		return check(C.mminj_mouse(C.int(t), C.double(in.x), C.double(in.y), C.int(e.Button), C.int64_t(cs), C.uint64_t(in.mods.flags(e.Mods))))
	case input.Key:
		code, ok := input.MacKeyCode(e.HID)
		if !ok {
			return fmt.Errorf("inject: no macOS key code for HID usage %#04x", e.HID)
		}
		isMod := in.mods.update(e.HID, e.Down)
		// For modifiers the event carries the state after the change.
		mods := e.Mods
		if isMod {
			mods = 0
		}
		return check(C.mminj_key(C.uint16_t(code), boolInt(e.Down), boolInt(isMod), C.uint64_t(in.mods.flags(mods))))
	case input.Wheel:
		dx, dy := in.scroll.add(e.DX, e.DY)
		if dx == 0 && dy == 0 {
			return nil
		}
		return check(C.mminj_scroll(C.int32_t(dx), C.int32_t(dy), C.uint64_t(in.mods.flags(e.Mods))))
	}
	return fmt.Errorf("inject: unknown event kind %d", e.Kind)
}

func check(r C.int) error {
	if r != 0 {
		return errors.New("inject: CGEvent creation failed")
	}
	return nil
}

func boolInt(b bool) C.int {
	if b {
		return 1
	}
	return 0
}

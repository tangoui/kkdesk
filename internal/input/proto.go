// Package input defines the binary wire format for remote input events sent
// from the viewer to the host over WebRTC data channels.
//
// Each message carries exactly one event. The first byte is the Kind; the
// remaining bytes are fixed-size little-endian fields that depend on the kind:
//
//	Move   X f32, Y f32                              (9 bytes)
//	Button X f32, Y f32, Button u8, Down u8, Mods u8 (12 bytes)
//	Key    HID u16, Down u8, Mods u8                 (5 bytes)
//	Wheel  DX f32, DY f32, Mods u8                   (10 bytes)
//
// X and Y are normalized to [0,1] relative to the host's main display. HID is
// a USB HID usage on the keyboard page (0x07). DX and DY are logical-pixel
// scroll deltas.
package input

import (
	"encoding/binary"
	"fmt"
	"math"
)

// Kind identifies the type of an input event. It is the first byte of every
// encoded event.
type Kind uint8

const (
	Move Kind = iota + 1
	Button
	Key
	Wheel
)

// Modifier bits for Event.Mods.
const (
	ModShift uint8 = 1 << iota
	ModCtrl
	ModAlt
	ModMeta
)

// Event is one remote input event. Which fields are meaningful depends on Kind;
// the others are zero after Unmarshal.
type Event struct {
	Kind   Kind
	X, Y   float32 // Move, Button: normalized position
	Button uint8   // Button: 0 left, 1 right, 2 middle, ...
	Down   bool    // Button, Key: pressed (true) or released (false)
	HID    uint16  // Key: USB HID usage (page 0x07)
	DX, DY float32 // Wheel: logical-pixel deltas
	Mods   uint8   // Button, Key, Wheel: ModShift | ModCtrl | ModAlt | ModMeta
}

// size returns the encoded length of an event of kind k, or 0 if k is unknown.
func size(k Kind) int {
	switch k {
	case Move:
		return 9
	case Button:
		return 12
	case Key:
		return 5
	case Wheel:
		return 10
	}
	return 0
}

// Marshal appends the encoding of e to dst and returns the extended slice.
// It panics if e.Kind is not a known kind.
func (e Event) Marshal(dst []byte) []byte {
	if size(e.Kind) == 0 {
		panic(fmt.Sprintf("input: marshal unknown kind %d", e.Kind))
	}
	dst = append(dst, byte(e.Kind))
	switch e.Kind {
	case Move:
		dst = appendF32(dst, e.X)
		dst = appendF32(dst, e.Y)
	case Button:
		dst = appendF32(dst, e.X)
		dst = appendF32(dst, e.Y)
		dst = append(dst, e.Button, boolByte(e.Down), e.Mods)
	case Key:
		dst = binary.LittleEndian.AppendUint16(dst, e.HID)
		dst = append(dst, boolByte(e.Down), e.Mods)
	case Wheel:
		dst = appendF32(dst, e.DX)
		dst = appendF32(dst, e.DY)
		dst = append(dst, e.Mods)
	}
	return dst
}

// MaxWheelDelta bounds one wheel event's delta (logical pixels) on decode.
const MaxWheelDelta = 10000

// Unmarshal decodes one event. It returns an error for an empty or unknown
// kind, for input that is not exactly the size of that kind, and for
// non-finite coordinates or deltas; wheel deltas are clamped to
// ±MaxWheelDelta.
func Unmarshal(b []byte) (Event, error) {
	if len(b) == 0 {
		return Event{}, fmt.Errorf("input: empty event")
	}
	k := Kind(b[0])
	n := size(k)
	if n == 0 {
		return Event{}, fmt.Errorf("input: unknown event kind %d", b[0])
	}
	if len(b) != n {
		return Event{}, fmt.Errorf("input: kind %d event is %d bytes, want %d", k, len(b), n)
	}
	e := Event{Kind: k}
	p := b[1:]
	switch k {
	case Move:
		e.X, e.Y = f32(p[0:]), f32(p[4:])
	case Button:
		e.X, e.Y = f32(p[0:]), f32(p[4:])
		e.Button, e.Down, e.Mods = p[8], p[9] != 0, p[10]
	case Key:
		e.HID = binary.LittleEndian.Uint16(p)
		e.Down, e.Mods = p[2] != 0, p[3]
	case Wheel:
		e.DX, e.DY = f32(p[0:]), f32(p[4:])
		e.Mods = p[8]
	}
	for _, v := range []float32{e.X, e.Y, e.DX, e.DY} {
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			return Event{}, fmt.Errorf("input: kind %d event has a non-finite value", k)
		}
	}
	e.DX = max(-MaxWheelDelta, min(e.DX, MaxWheelDelta))
	e.DY = max(-MaxWheelDelta, min(e.DY, MaxWheelDelta))
	return e, nil
}

func appendF32(dst []byte, v float32) []byte {
	return binary.LittleEndian.AppendUint32(dst, math.Float32bits(v))
}

func f32(b []byte) float32 {
	return math.Float32frombits(binary.LittleEndian.Uint32(b))
}

func boolByte(v bool) byte {
	if v {
		return 1
	}
	return 0
}

package input

import (
	"math"
	"bytes"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		ev   Event
		size int
	}{
		{"move", Event{Kind: Move, X: 0.25, Y: 0.875}, 9},
		{"move corner", Event{Kind: Move, X: 1, Y: 0}, 9},
		{"button down", Event{Kind: Button, X: 0.5, Y: 0.125, Button: 1, Down: true, Mods: ModShift | ModMeta}, 12},
		{"button up", Event{Kind: Button, X: 0.75, Y: 1, Button: 2}, 12},
		{"key down", Event{Kind: Key, HID: 0x04, Down: true, Mods: ModCtrl}, 5},
		{"key up", Event{Kind: Key, HID: 0xE7, Mods: ModAlt | ModShift}, 5},
		{"wheel", Event{Kind: Wheel, DX: -3.5, DY: 120, Mods: ModCtrl}, 10},
	}
	for _, c := range cases {
		b := c.ev.Marshal(nil)
		if len(b) != c.size {
			t.Errorf("%s: encoded %d bytes, want %d", c.name, len(b), c.size)
		}
		if Kind(b[0]) != c.ev.Kind {
			t.Errorf("%s: first byte %d, want kind %d", c.name, b[0], c.ev.Kind)
		}
		got, err := Unmarshal(b)
		if err != nil {
			t.Fatalf("%s: Unmarshal: %v", c.name, err)
		}
		if got != c.ev {
			t.Errorf("%s: round trip = %+v, want %+v", c.name, got, c.ev)
		}
	}
}

func TestMarshalAppends(t *testing.T) {
	prefix := []byte{0xAA, 0xBB}
	b := Event{Kind: Key, HID: 0x28, Down: true}.Marshal(append([]byte(nil), prefix...))
	if !bytes.Equal(b[:2], prefix) {
		t.Fatalf("prefix clobbered: % x", b)
	}
	got, err := Unmarshal(b[2:])
	if err != nil || got.HID != 0x28 || !got.Down {
		t.Fatalf("Unmarshal appended = %+v, %v", got, err)
	}
}

func TestLittleEndianLayout(t *testing.T) {
	b := Event{Kind: Key, HID: 0x1234, Down: true, Mods: ModMeta}.Marshal(nil)
	want := []byte{byte(Key), 0x34, 0x12, 1, ModMeta}
	if !bytes.Equal(b, want) {
		t.Fatalf("key layout = % x, want % x", b, want)
	}
	// 1.0f = 0x3F800000, little-endian.
	b = Event{Kind: Move, X: 1, Y: 0}.Marshal(nil)
	want = []byte{byte(Move), 0, 0, 0x80, 0x3F, 0, 0, 0, 0}
	if !bytes.Equal(b, want) {
		t.Fatalf("move layout = % x, want % x", b, want)
	}
}

func TestUnmarshalTruncated(t *testing.T) {
	events := []Event{
		{Kind: Move, X: 0.1, Y: 0.2},
		{Kind: Button, X: 0.1, Y: 0.2, Button: 1, Down: true},
		{Kind: Key, HID: 0x04, Down: true},
		{Kind: Wheel, DX: 1, DY: 2},
	}
	for _, ev := range events {
		b := ev.Marshal(nil)
		for n := 0; n < len(b); n++ {
			if _, err := Unmarshal(b[:n]); err == nil {
				t.Errorf("kind %d: Unmarshal of %d/%d bytes succeeded, want error", ev.Kind, n, len(b))
			}
		}
	}
}

func TestUnmarshalTrailingBytes(t *testing.T) {
	b := Event{Kind: Key, HID: 0x04}.Marshal(nil)
	if _, err := Unmarshal(append(b, 0)); err == nil {
		t.Fatal("Unmarshal with trailing byte succeeded, want error")
	}
}

func TestUnmarshalUnknownKind(t *testing.T) {
	for _, k := range []byte{0, 5, 0x7F, 0xFF} {
		b := make([]byte, 16)
		b[0] = k
		if _, err := Unmarshal(b); err == nil {
			t.Errorf("Unmarshal with kind %d succeeded, want error", k)
		}
	}
}

// Non-finite values from the network are refused, and wheel deltas are
// bounded, so one bad event cannot poison the host's scroll accumulators or
// pointer position for the rest of the session.
func TestUnmarshalRejectsNonFiniteAndBoundsWheel(t *testing.T) {
	nan, inf := float32(math.NaN()), float32(math.Inf(1))
	for _, e := range []Event{
		{Kind: Move, X: nan, Y: 0.5},
		{Kind: Move, X: 0.5, Y: inf},
		{Kind: Button, X: nan, Y: 0, Button: 0, Down: true},
		{Kind: Wheel, DX: 0, DY: nan},
		{Kind: Wheel, DX: inf, DY: 0},
	} {
		if _, err := Unmarshal(e.Marshal(nil)); err == nil {
			t.Errorf("Unmarshal accepted %+v", e)
		}
	}
	got, err := Unmarshal(Event{Kind: Wheel, DX: -1e30, DY: 1e30}.Marshal(nil))
	if err != nil {
		t.Fatal(err)
	}
	if got.DX != -MaxWheelDelta || got.DY != MaxWheelDelta {
		t.Fatalf("wheel = %v, %v; want ±%v", got.DX, got.DY, MaxWheelDelta)
	}
}

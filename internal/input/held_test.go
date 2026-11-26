package input

import (
	"reflect"
	"testing"
)

func TestHeldReleasesWhatIsDown(t *testing.T) {
	var h Held
	h.Track(Event{Kind: Key, HID: 0xE1, Down: true}) // left shift
	h.Track(Event{Kind: Key, HID: 0x04, Down: true}) // a
	h.Track(Event{Kind: Key, HID: 0x05, Down: true}) // b
	h.Track(Event{Kind: Key, HID: 0x05})             // b up
	h.Track(Event{Kind: Button, X: 0.1, Y: 0.2, Button: 0, Down: true})
	h.Track(Event{Kind: Move, X: 0.5, Y: 0.6})
	h.Track(Event{Kind: Button, Button: 1, X: 0.5, Y: 0.6, Down: true})
	h.Track(Event{Kind: Button, Button: 1, X: 0.5, Y: 0.6})
	got := h.Release()
	want := []Event{
		{Kind: Button, X: 0.5, Y: 0.6, Button: 0},
		{Kind: Key, HID: 0x04},
		{Kind: Key, HID: 0xE1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Release = %+v, want %+v", got, want)
	}
	if again := h.Release(); len(again) != 0 {
		t.Fatalf("second Release = %+v, want nothing", again)
	}
}

func TestHeldRepeatDownOnce(t *testing.T) {
	var h Held
	h.Track(Event{Kind: Key, HID: 0x04, Down: true})
	h.Track(Event{Kind: Key, HID: 0x04, Down: true}) // auto-repeat
	if got := h.Release(); len(got) != 1 {
		t.Fatalf("Release = %+v, want one key up", got)
	}
}

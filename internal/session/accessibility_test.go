package session

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pion/webrtc/v4"
)

func TestAccessibilityGolden(t *testing.T) {
	for _, tc := range []struct {
		name string
		want bool
	}{
		{"denied", false},
		{"granted", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			golden, err := os.ReadFile(filepath.Join("..", "..", "mobile", "test", "fixtures", "meta", "accessibility-"+tc.name+".bin"))
			if err != nil {
				t.Fatal(err)
			}
			if b := encodeAccessibility(tc.want); !bytes.Equal(b, golden) {
				t.Fatalf("encoded %x, want %x", b, golden)
			}
			if got, ok := decodeAccessibility(golden); !ok || got != tc.want {
				t.Fatalf("decoded %v, %v, want %v", got, ok, tc.want)
			}
		})
	}
}

func TestAccessibilityMeta(t *testing.T) {
	host, controller := connect(t)
	if controller.AccessibilityDenied() {
		t.Fatal("an older host that sends no permission status must still allow input")
	}
	// Unknown and malformed messages must leave the current state alone.
	for _, b := range [][]byte{nil, {'?'}, {'a'}, {'a', 2}, {'a', 0, 0}} {
		controller.handleMeta(webrtc.DataChannelMessage{Data: b})
	}
	controller.handleMeta(webrtc.DataChannelMessage{Data: []byte{'a', 0}, IsString: true})
	if controller.AccessibilityDenied() {
		t.Fatal("invalid or unrelated metadata changed the permission")
	}

	changed := make(chan bool, 2)
	controller.OnAccessibilityChange(func() { changed <- controller.AccessibilityDenied() })
	for _, granted := range []bool{false, true} {
		if err := host.SendAccessibility(granted); err != nil {
			t.Fatal(err)
		}
		select {
		case denied := <-changed:
			if denied == granted {
				t.Fatalf("controller reported denied=%v after host sent granted=%v", denied, granted)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("accessibility update did not arrive")
		}
	}
	if err := controller.SendAccessibility(false); err == nil {
		t.Fatal("controller must not publish host permission")
	}
	host.handleMeta(webrtc.DataChannelMessage{Data: []byte{'a', 0}})
	if host.AccessibilityDenied() {
		t.Fatal("host accepted controller's permission status")
	}
}

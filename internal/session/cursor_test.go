package session

import (
	"testing"
	"time"

	"github.com/pion/webrtc/v4"
)

func TestCursorMetaAdmission(t *testing.T) {
	for _, tc := range []struct {
		name    string
		host    bool
		mode    Mode
		pending bool
		text    bool
		data    []byte
		want    CursorShape
	}{
		{name: "authenticated", data: []byte{metaCursor, byte(CursorPointer)}, want: CursorPointer},
		{name: "pending password", pending: true, data: []byte{metaCursor, byte(CursorPointer)}},
		{name: "wrong direction", host: true, data: []byte{metaCursor, byte(CursorPointer)}},
		{name: "files only", mode: ModeFiles, data: []byte{metaCursor, byte(CursorPointer)}},
		{name: "text", text: true, data: []byte{metaCursor, byte(CursorPointer)}},
		{name: "short", data: []byte{metaCursor}},
		{name: "long", data: []byte{metaCursor, byte(CursorPointer), 0}},
		{name: "unknown", data: []byte{metaCursor, 0xff}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &Peer{isHost: tc.host, mode: tc.mode}
			if tc.pending {
				p.requirePassword()
			}
			p.handleMeta(webrtc.DataChannelMessage{Data: tc.data, IsString: tc.text})
			if got := p.Cursor(); got != tc.want {
				t.Fatalf("cursor = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCursorMetaRoundTrip(t *testing.T) {
	host, viewer := connect(t)
	changes := make(chan CursorShape, 16)
	viewer.OnCursorChange(func() { changes <- viewer.Cursor() })
	host.requirePassword()
	if err := host.SendCursor(CursorPointer); err == nil {
		t.Fatal("host sent a cursor before authentication")
	}
	host.auth.Load().verified.Store(true)
	if err := viewer.SendCursor(CursorPointer); err == nil {
		t.Fatal("controller sent a cursor")
	}
	for _, want := range []CursorShape{CursorPointer, CursorText, CursorNotAllowed, CursorResizeEW, CursorResizeNS, CursorMove, CursorGrab, CursorGrabbing, CursorDefault} {
		if err := host.SendCursor(want); err != nil {
			t.Fatal(err)
		}
		select {
		case got := <-changes:
			if got != want {
				t.Fatalf("cursor = %v, want %v", got, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out waiting for cursor %v", want)
		}
	}
}

func TestCursorMetaFilesSession(t *testing.T) {
	host, _ := connectMode(t, DialOptions{Mode: ModeFiles})
	if err := host.SendCursor(CursorPointer); err == nil {
		t.Fatal("files-only host sent a cursor")
	}
}

func TestCursorMetaKeepsLatestShape(t *testing.T) {
	p := &Peer{}
	update := func(shape byte) {
		p.handleMeta(webrtc.DataChannelMessage{Data: []byte{metaCursor, shape}})
	}
	update(byte(CursorText))
	seen := 0
	p.OnCursorChange(func() { seen++ })
	if got := p.Cursor(); got != CursorText {
		t.Fatalf("cursor before subscription = %v, want text", got)
	}
	update(byte(CursorText))
	update(0xff)
	if got := p.Cursor(); got != CursorDefault || seen != 1 {
		t.Fatalf("unknown cursor = %v with %d callbacks, want arrow and one callback", got, seen)
	}
}

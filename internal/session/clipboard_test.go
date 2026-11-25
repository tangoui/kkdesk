package session

import (
	"strings"
	"testing"
	"time"
)

// Clipboard text goes both ways over the meta channel; text longer than one
// chunk (and with multi-byte characters cut across chunks) arrives whole.
func TestClipboardBothWays(t *testing.T) {
	host, ctl := connect(t)
	toHost := make(chan string, 4)
	toCtl := make(chan string, 4)
	host.OnClipboard(func(s string) { toHost <- s })
	ctl.OnClipboard(func(s string) { toCtl <- s })

	long := strings.Repeat("远程桌面 clipboard ", 6000) // ~130 KB, many chunks
	for _, tc := range []struct {
		from *Peer
		to   chan string
		text string
	}{
		{ctl, toHost, "hello 你好"},
		{host, toCtl, long},
		{ctl, toHost, ""}, // an emptied clipboard is a change too
	} {
		if err := tc.from.SendClipboard(tc.text); err != nil {
			t.Fatal(err)
		}
		select {
		case got := <-tc.to:
			if got != tc.text {
				t.Fatalf("got %d bytes, want %d", len(got), len(tc.text))
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("clipboard of %d bytes not delivered", len(tc.text))
		}
	}
}

func TestClipboardTooLong(t *testing.T) {
	_, ctl := connect(t)
	if err := ctl.SendClipboard(strings.Repeat("x", MaxClipboard+1)); err == nil {
		t.Fatal("text over MaxClipboard was sent")
	}
}

// Without a handler (the host before the connection is accepted) clipboard
// text is dropped, not kept for later.
func TestClipboardDroppedWithoutHandler(t *testing.T) {
	host, ctl := connect(t)
	if err := ctl.SendClipboard("early"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	got := make(chan string, 2)
	host.OnClipboard(func(s string) { got <- s })
	if err := ctl.SendClipboard("late"); err != nil {
		t.Fatal(err)
	}
	if s := <-got; s != "late" {
		t.Fatalf("first delivered %q, want late", s)
	}
}

// A reassembly that is interrupted (a new text starts before the last chunk)
// is dropped rather than glued to the next text.
func TestClipAssembler(t *testing.T) {
	var a clipAssembler
	if _, ok := a.add(clipChunk(1, false, []byte("ab"))); ok {
		t.Fatal("partial text delivered")
	}
	s, ok := a.add(clipChunk(2, true, []byte("xy")))
	if !ok || s != "xy" {
		t.Fatalf("got %q %v, want xy", s, ok)
	}
	if _, ok := a.add([]byte{metaClip, 1}); ok {
		t.Fatal("malformed chunk delivered")
	}
}

// A text over MaxClipboard is dropped whole: its remaining chunks are not
// taken for a new text (a peer that ignores the limit).
func TestClipAssemblerOverLimit(t *testing.T) {
	var a clipAssembler
	big := make([]byte, clipChunkSize)
	for i := 0; i*clipChunkSize <= MaxClipboard; i++ {
		if _, ok := a.add(clipChunk(7, false, big)); ok {
			t.Fatal("delivered while over the limit")
		}
	}
	if s, ok := a.add(clipChunk(7, true, []byte("tail"))); ok {
		t.Fatalf("delivered %d bytes of a dropped text", len(s))
	}
	if s, ok := a.add(clipChunk(8, true, []byte("next"))); !ok || s != "next" {
		t.Fatalf("next text: %q %v", s, ok)
	}
}

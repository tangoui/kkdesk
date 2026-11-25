package clipsync

import (
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeBoard is a clipboard in memory.
type fakeBoard struct {
	mu   sync.Mutex
	text string
	err  error
}

func (b *fakeBoard) ReadText() (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.text, b.err
}

func (b *fakeBoard) WriteText(s string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.text = s
	return nil
}

func (b *fakeBoard) set(s string) { b.mu.Lock(); b.text = s; b.mu.Unlock() }

// sent collects what the engine sends.
type sent struct {
	mu  sync.Mutex
	got []string
}

func (s *sent) send(t string) error { s.mu.Lock(); s.got = append(s.got, t); s.mu.Unlock(); return nil }
func (s *sent) list() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.got...)
}

const tick = 10 * time.Millisecond

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(tick)
	}
}

// What is on the clipboard when the session starts is not sent; a copy made
// afterwards is, once.
func TestSendsChangesOnly(t *testing.T) {
	b := &fakeBoard{text: "before the session"}
	var out sent
	s := Start(Options{Local: b, Send: out.send, Interval: tick})
	defer s.Stop()
	time.Sleep(5 * tick)
	if got := out.list(); len(got) != 0 {
		t.Fatalf("sent %q at start", got)
	}
	b.set("copied")
	waitFor(t, "the copy to be sent", func() bool { return len(out.list()) == 1 })
	time.Sleep(5 * tick)
	if got := out.list(); len(got) != 1 || got[0] != "copied" {
		t.Fatalf("sent %q, want [copied] once", got)
	}
}

// Text from the other side lands on the clipboard and is not sent back.
func TestReceiveDoesNotEcho(t *testing.T) {
	b := &fakeBoard{}
	var out sent
	s := Start(Options{Local: b, Send: out.send, Interval: tick})
	defer s.Stop()
	s.Receive("from the other side")
	if got, _ := b.ReadText(); got != "from the other side" {
		t.Fatalf("clipboard %q", got)
	}
	time.Sleep(5 * tick)
	if got := out.list(); len(got) != 0 {
		t.Fatalf("echoed %q", got)
	}
}

// With the permission off nothing is sent or applied; turning it back on
// does not send the copy made meanwhile, only the next one.
func TestNotAllowed(t *testing.T) {
	b := &fakeBoard{}
	var out sent
	var allowed atomic.Bool
	s := Start(Options{Local: b, Send: out.send, Interval: tick, Allowed: allowed.Load})
	defer s.Stop()
	s.Receive("blocked")
	if got, _ := b.ReadText(); got != "" {
		t.Fatalf("applied %q while not allowed", got)
	}
	b.set("private")
	time.Sleep(5 * tick)
	allowed.Store(true)
	time.Sleep(5 * tick)
	if got := out.list(); len(got) != 0 {
		t.Fatalf("sent %q", got)
	}
	b.set("shared")
	waitFor(t, "the next copy", func() bool { return len(out.list()) == 1 })
	if got := out.list()[0]; got != "shared" {
		t.Fatalf("sent %q", got)
	}
}

// Text over the limit and read errors are skipped without stopping the
// engine.
func TestSkipsTooLongAndErrors(t *testing.T) {
	b := &fakeBoard{}
	var out sent
	s := Start(Options{Local: b, Send: out.send, Interval: tick, MaxLen: 8})
	defer s.Stop()
	b.set(strings.Repeat("x", 9))
	time.Sleep(5 * tick)
	b.mu.Lock()
	b.err = errors.New("busy")
	b.mu.Unlock()
	time.Sleep(5 * tick)
	b.mu.Lock()
	b.err, b.text = nil, "ok"
	b.mu.Unlock()
	waitFor(t, "the short copy", func() bool { return len(out.list()) == 1 })
	if got := out.list()[0]; got != "ok" {
		t.Fatalf("sent %q", got)
	}
}

// After Stop the clipboard is neither read nor written.
func TestStop(t *testing.T) {
	b := &fakeBoard{}
	var out sent
	s := Start(Options{Local: b, Send: out.send, Interval: tick})
	s.Stop()
	s.Receive("late")
	b.set("after")
	time.Sleep(5 * tick)
	if got, _ := b.ReadText(); got != "after" || len(out.list()) != 0 {
		t.Fatalf("clipboard %q, sent %q after Stop", got, out.list())
	}
}

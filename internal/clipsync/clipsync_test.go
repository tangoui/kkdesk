package clipsync

import (
	"errors"
	"fmt"
	"io"
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
	waitFor(t, "the received text", func() bool { got, _ := b.ReadText(); return got == "from the other side" })
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
	time.Sleep(5 * tick)
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

// An image or a file list reads as "": it is not sent (it would empty the
// other clipboard).
func TestEmptyNotSent(t *testing.T) {
	b := &fakeBoard{text: "text"}
	var out sent
	s := Start(Options{Local: b, Send: out.send, Interval: tick})
	defer s.Stop()
	b.set("")
	time.Sleep(5 * tick)
	if got := out.list(); len(got) != 0 {
		t.Fatalf("sent %q", got)
	}
}

// slowBoard blocks writes until released.
type slowBoard struct {
	fakeBoard
	release chan struct{}
}

func (b *slowBoard) WriteText(s string) error {
	<-b.release
	return b.fakeBoard.WriteText(s)
}

// Receive returns at once even while the clipboard is slow (it runs on the
// session's meta channel, which also carries the clock sync), and the
// newest text wins.
func TestReceiveDoesNotBlock(t *testing.T) {
	b := &slowBoard{release: make(chan struct{})}
	s := Start(Options{Local: b, Send: func(string) error { return nil }, Interval: tick})
	defer s.Stop()
	done := make(chan struct{})
	go func() {
		for _, x := range []string{"1", "2", "3", "4"} {
			s.Receive(x)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Receive blocked on a slow clipboard")
	}
	close(b.release)
	waitFor(t, "the newest text", func() bool { got, _ := b.ReadText(); return got == "4" })
}

// A copy whose send fails is tried again on the next ticks until it goes
// through, then sent no more.
func TestRetriesFailedSend(t *testing.T) {
	b := &fakeBoard{}
	var fails atomic.Int32
	fails.Store(2)
	var out sent
	send := func(text string) error {
		if fails.Add(-1) >= 0 {
			return errors.New("channel busy")
		}
		return out.send(text)
	}
	s := Start(Options{Local: b, Send: send, Interval: tick})
	defer s.Stop()
	b.set("copied")
	waitFor(t, "the copy to be sent after failures", func() bool { return len(out.list()) == 1 })
	time.Sleep(5 * tick)
	if got := out.list(); len(got) != 1 || got[0] != "copied" {
		t.Fatalf("sent %q, want [copied] once", got)
	}
}

func TestClosedPipeStopsSending(t *testing.T) {
	b := &fakeBoard{}
	var attempts atomic.Int32
	s := Start(Options{Local: b, Interval: tick, Send: func(string) error {
		attempts.Add(1)
		return fmt.Errorf("transport: %w", io.ErrClosedPipe)
	}})
	defer s.Stop()
	b.set("first copy")
	waitFor(t, "first send", func() bool { return attempts.Load() > 0 })
	b.set("second copy")
	time.Sleep(5 * tick)
	if got := attempts.Load(); got != 1 {
		t.Fatalf("send attempts = %d, want 1 after terminal failure", got)
	}
}

func TestSendErrorsDeduplicatedAcrossPolls(t *testing.T) {
	b := &fakeBoard{}
	var attempts atomic.Int32
	var logs sent
	s := Start(Options{Local: b, Interval: tick, Send: func(string) error {
		switch attempts.Add(1) {
		case 1, 2:
			return errors.New("channel busy")
		case 3, 4:
			return errors.New("channel blocked")
		default:
			return nil
		}
	}, Logf: func(format string, args ...any) { _ = logs.send(fmt.Sprintf(format, args...)) }})
	defer s.Stop()
	b.set("copied")
	waitFor(t, "successful retry", func() bool { return attempts.Load() >= 5 })
	s.Stop()
	if got := strings.Join(logs.list(), "\n"); got != "send clipboard: channel busy\nsend clipboard: channel blocked" {
		t.Fatalf("logged %q", got)
	}
}

// A pending copy is dropped when syncing is turned off.
func TestPendingDroppedWhenNotAllowed(t *testing.T) {
	b := &fakeBoard{}
	var allowed atomic.Bool
	allowed.Store(true)
	var failing atomic.Bool
	failing.Store(true)
	var out sent
	send := func(text string) error {
		if failing.Load() {
			return errors.New("channel busy")
		}
		return out.send(text)
	}
	s := Start(Options{Local: b, Send: send, Allowed: allowed.Load, Interval: tick})
	defer s.Stop()
	b.set("copied")
	time.Sleep(5 * tick)
	allowed.Store(false)
	time.Sleep(5 * tick)
	failing.Store(false)
	allowed.Store(true)
	time.Sleep(10 * tick)
	if got := out.list(); len(got) != 0 {
		t.Fatalf("sent %q after the permission was off", got)
	}
}

// Package clipsync keeps the clipboard text of the two ends of a session in
// step. Each end polls its own clipboard, sends a change to the other side
// and writes what it receives; what was received is remembered so it is not
// sent back. The clipboard at the start of a session is not sent.
package clipsync

import (
	"sync"
	"time"
)

// Clipboard reads and writes the local clipboard text.
type Clipboard interface {
	ReadText() (string, error)
	WriteText(string) error
}

// Options configures Start.
type Options struct {
	Local Clipboard
	// Send delivers a local change to the other side.
	Send func(string) error
	// Allowed reports whether syncing is allowed now (the host's 剪贴板
	// permission, a controller that is not view-only); nil means always.
	Allowed func() bool
	// Interval between reads of the local clipboard; 0 means 500 ms.
	Interval time.Duration
	// MaxLen skips local text longer than this many bytes; 0 means no limit.
	MaxLen int
	// Logf reports read, write and send errors; nil discards them.
	Logf func(format string, args ...any)
}

// Sync is a running clipboard sync. Its methods are safe for concurrent
// use.
type Sync struct {
	o    Options
	stop chan struct{}
	done chan struct{}
	once sync.Once
	recv chan string // received text, applied by loop (the latest wins)

	// mu is held across a read and the comparison (poll) and across a
	// write (Receive): otherwise a read from before a received text could
	// be taken for a new copy and sent back.
	mu         sync.Mutex
	last       string // the clipboard text as last seen, written or sent
	known      bool   // last is valid
	stopped    bool
	pending    string // a local change not yet delivered (Send failed)
	hasPending bool
	errLast    string // the last error logged, to log each kind once
}

// Start reads the clipboard once (that text is not sent) and polls it
// until Stop.
func Start(o Options) *Sync {
	if o.Interval <= 0 {
		o.Interval = 500 * time.Millisecond
	}
	s := &Sync{o: o, stop: make(chan struct{}), done: make(chan struct{}), recv: make(chan string, 1)}
	s.mu.Lock()
	if t, err := o.Local.ReadText(); err == nil {
		s.last, s.known = t, true
	}
	s.mu.Unlock()
	go s.loop()
	return s
}

// Stop ends polling; Receive does nothing afterwards.
func (s *Sync) Stop() {
	s.once.Do(func() {
		close(s.stop)
		<-s.done
		s.mu.Lock()
		s.stopped = true
		s.mu.Unlock()
	})
}

// Receive queues text from the other side for the local clipboard, if
// allowed. It does not wait for the clipboard: the caller is the session's
// meta channel, which also carries the clock sync. A text not yet written
// is replaced by a newer one.
func (s *Sync) Receive(text string) {
	if !s.allowed() {
		return
	}
	for {
		select {
		case <-s.stop:
			return
		case s.recv <- text:
			return
		default:
			select { // drop the older queued text
			case <-s.recv:
			default:
			}
		}
	}
}

// apply writes received text to the local clipboard (loop goroutine).
func (s *Sync) apply(text string) {
	if !s.allowed() {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped || (s.known && text == s.last) {
		return
	}
	if err := s.o.Local.WriteText(text); err != nil {
		s.logLocked("write clipboard: %v", err)
		return
	}
	s.last, s.known = text, true
}

func (s *Sync) allowed() bool { return s.o.Allowed == nil || s.o.Allowed() }

func (s *Sync) loop() {
	defer close(s.done)
	t := time.NewTicker(s.o.Interval)
	defer t.Stop()
	for {
		select {
		case <-s.stop:
			return
		case text := <-s.recv:
			s.apply(text)
		case <-t.C:
			if text, ok := s.poll(); ok {
				s.send(text)
			} else if text, ok := s.retry(); ok {
				s.send(text)
			}
		}
	}
}

// send delivers a local change; a failed one is kept and tried again on the
// next ticks until it goes through or a newer text replaces it.
func (s *Sync) send(text string) {
	err := s.o.Send(text)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		s.logLocked("send clipboard: %v", err)
		s.pending, s.hasPending = text, true
		return
	}
	if s.hasPending && s.pending == text {
		s.pending, s.hasPending = "", false
	}
}

// retry returns the change whose send failed, if it may still be sent.
func (s *Sync) retry() (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.hasPending {
		return "", false
	}
	if !s.allowed() || s.pending != s.last {
		// Turned off, or the clipboard moved on (a newer copy, or text
		// received from the other side): the failed copy is stale.
		s.pending, s.hasPending = "", false
		return "", false
	}
	return s.pending, true
}

// poll reads the clipboard and returns a change to send.
func (s *Sync) poll() (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	text, err := s.o.Local.ReadText()
	if err != nil {
		s.logLocked("read clipboard: %v", err)
		return "", false
	}
	s.errLast = ""                       // log the next failure again
	changed := s.known && text != s.last // the first read is the baseline
	s.last, s.known = text, true
	// A change made while not allowed is taken as seen: turning the
	// permission on later does not send it.
	// "" is also what an image or a file list reads as: sending it would
	// empty the other clipboard.
	if !changed || text == "" || !s.allowed() || (s.o.MaxLen > 0 && len(text) > s.o.MaxLen) {
		return "", false
	}
	s.pending, s.hasPending = "", false // superseded by this copy
	return text, true
}

func (s *Sync) logLocked(format string, args ...any) {
	if s.o.Logf == nil || format == s.errLast {
		return
	}
	s.errLast = format
	s.o.Logf(format, args...)
}

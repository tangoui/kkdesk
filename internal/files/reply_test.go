package files

import (
	"testing"
	"time"
)

// A host that repeats a reply cannot block the client's message goroutine
// (which would stall every transfer and request on the channel).
func TestDuplicateRepliesDoNotBlock(t *testing.T) {
	r := newRig(t)
	calls := 0
	pc := &pendingCall{ch: make(chan *msg, 1), onReply: func(*msg) { calls++ }}
	r.c.mu.Lock()
	r.c.pending[777] = pc
	r.c.mu.Unlock()
	done := make(chan struct{})
	go func() {
		for range 3 {
			m := okMsg(777)
			r.c.onJSON(&m)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("onJSON blocked on a repeated reply")
	}
	if calls != 1 {
		t.Fatalf("onReply ran %d times, want once", calls)
	}
}

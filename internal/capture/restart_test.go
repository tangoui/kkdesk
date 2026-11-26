package capture

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestRestarterRetriesUntilReopened(t *testing.T) {
	var calls atomic.Int32
	r := &restarter{delays: []time.Duration{time.Millisecond}, reopen: func() error {
		if calls.Add(1) < 3 {
			return errors.New("not yet")
		}
		return nil
	}}
	r.trigger("stopped")
	waitFor(t, "a successful reopen", func() bool { return r.reopened() == 1 })
	if n := calls.Load(); n != 3 {
		t.Fatalf("reopen called %d times, want 3 (two failures, then success)", n)
	}
	r.close()
}

func TestRestarterIgnoresTriggersWhileRunning(t *testing.T) {
	release := make(chan struct{})
	var calls atomic.Int32
	r := &restarter{delays: []time.Duration{time.Millisecond}, reopen: func() error {
		calls.Add(1)
		<-release
		return nil
	}}
	r.trigger("stopped")
	waitFor(t, "the first reopen", func() bool { return calls.Load() == 1 })
	r.trigger("stopped again")
	r.trigger("and again")
	close(release)
	waitFor(t, "the reopen to finish", func() bool { return r.reopened() == 1 })
	time.Sleep(20 * time.Millisecond)
	if n := calls.Load(); n != 1 {
		t.Fatalf("reopen called %d times for triggers during one restart, want 1", n)
	}
	r.close()
}

func TestRestarterCloseStopsRetriesAndWaits(t *testing.T) {
	var calls atomic.Int32
	r := &restarter{delays: []time.Duration{5 * time.Millisecond}, reopen: func() error {
		calls.Add(1)
		return errors.New("keeps failing")
	}}
	r.trigger("stopped")
	waitFor(t, "a few attempts", func() bool { return calls.Load() >= 2 })
	done := make(chan struct{})
	go func() { r.close(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("close did not return while reopen kept failing")
	}
	n := calls.Load()
	time.Sleep(30 * time.Millisecond)
	if calls.Load() != n {
		t.Fatal("reopen was still retried after close returned")
	}
	r.trigger("after close")
	time.Sleep(20 * time.Millisecond)
	if calls.Load() != n {
		t.Fatal("trigger after close started a restart")
	}
}

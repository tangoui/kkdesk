package app

import (
	"context"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestProberOnlineOffline(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	// A port that was free a moment ago refuses connections.
	dead, _ := net.Listen("tcp", "127.0.0.1:0")
	deadAddr := dead.Addr().String()
	dead.Close()

	var notified atomic.Int32
	p := NewProber(ProberConfig{Interval: time.Hour}, func() { notified.Add(1) })
	defer p.Close()
	if got := p.Status("x"); got != ProbeUnknown {
		t.Fatalf("status of an unknown address %v", got)
	}
	p.SetTargets([]string{ln.Addr().String(), deadAddr})
	if got := p.Status(ln.Addr().String()); got != ProbeUnknown && got != ProbeOnline {
		t.Fatalf("before the first round %v", got)
	}
	waitFor(t, "online", func() bool { return p.Status(ln.Addr().String()) == ProbeOnline })
	waitFor(t, "offline", func() bool { return p.Status(deadAddr) == ProbeOffline })
	if notified.Load() == 0 {
		t.Fatal("notify not called")
	}
	// Removed targets are forgotten.
	p.SetTargets([]string{deadAddr})
	if got := p.Status(ln.Addr().String()); got != ProbeUnknown {
		t.Fatalf("removed target still %v", got)
	}
}

// blockingDial blocks until the context ends and records concurrency.
type blockingDial struct {
	mu        sync.Mutex
	cur, peak int
	calls     atomic.Int32
}

func (b *blockingDial) dial(ctx context.Context, addr string) error {
	b.calls.Add(1)
	b.mu.Lock()
	b.cur++
	b.peak = max(b.peak, b.cur)
	b.mu.Unlock()
	<-ctx.Done()
	b.mu.Lock()
	b.cur--
	b.mu.Unlock()
	return ctx.Err()
}

func TestProberTimeoutAndCap(t *testing.T) {
	b := &blockingDial{}
	p := NewProber(ProberConfig{Interval: time.Hour, Timeout: 50 * time.Millisecond, Limit: 3, Dial: b.dial}, nil)
	defer p.Close()
	var addrs []string
	for i := range 10 {
		addrs = append(addrs, net.JoinHostPort("10.255.0."+strconv.Itoa(i+1), "21118"))
	}
	start := time.Now()
	p.SetTargets(addrs)
	waitFor(t, "all offline", func() bool {
		for _, a := range addrs {
			if p.Status(a) != ProbeOffline {
				return false
			}
		}
		return true
	})
	b.mu.Lock()
	peak := b.peak
	b.mu.Unlock()
	if peak > 3 {
		t.Fatalf("%d probes at once, cap 3", peak)
	}
	// 10 probes, 3 at a time, 50 ms each: at least 4 waves.
	if el := time.Since(start); el < 4*50*time.Millisecond {
		t.Fatalf("finished in %v: timeout or cap not applied", el)
	}
}

func TestProberStopsOnClose(t *testing.T) {
	b := &blockingDial{}
	p := NewProber(ProberConfig{Interval: 10 * time.Millisecond, Timeout: time.Hour, Limit: 2, Dial: b.dial}, nil)
	p.SetTargets([]string{"10.255.0.1:1", "10.255.0.2:1", "10.255.0.3:1"})
	waitFor(t, "probing", func() bool { return b.calls.Load() >= 2 })
	done := make(chan struct{})
	go func() { p.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Close blocked on running probes")
	}
	n := b.calls.Load()
	time.Sleep(50 * time.Millisecond)
	if b.calls.Load() != n {
		t.Fatal("probing went on after Close")
	}
	p.Close() // twice is fine
	p.SetTargets([]string{"10.255.0.9:1"})
	time.Sleep(20 * time.Millisecond)
	if b.calls.Load() != n {
		t.Fatal("SetTargets after Close probed")
	}
}

func TestProberPeriodic(t *testing.T) {
	var calls atomic.Int32
	dial := func(ctx context.Context, addr string) error { calls.Add(1); return nil }
	p := NewProber(ProberConfig{Interval: 20 * time.Millisecond, Dial: dial}, nil)
	defer p.Close()
	p.SetTargets([]string{"a:1"})
	waitFor(t, "several rounds", func() bool { return calls.Load() >= 3 })
}

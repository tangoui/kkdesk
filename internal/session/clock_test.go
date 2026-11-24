package session

import (
	"testing"
	"time"
)

// exchange simulates one ping/pong: the viewer sends at viewer time t0, the
// ping takes up to the host whose clock is viewer+offset, the pong takes down
// back. It returns the three timestamps the estimator sees.
func exchange(t0 time.Time, offset, up, down time.Duration) (int64, int64, int64) {
	recv := t0.Add(up) // viewer time when the host receives the ping
	return t0.UnixNano(), recv.Add(offset).UnixNano(), recv.Add(down).UnixNano()
}

func TestClockSyncEstimate(t *testing.T) {
	base := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	for _, offset := range []time.Duration{3 * time.Second, -250 * time.Millisecond, 0} {
		var c ClockSync
		if _, _, ok := c.Offset(); ok {
			t.Fatal("ok before any sample")
		}
		delays := []struct{ up, down time.Duration }{
			{40 * time.Millisecond, 5 * time.Millisecond},
			{3 * time.Millisecond, 9 * time.Millisecond}, // min rtt: 12 ms
			{20 * time.Millisecond, 20 * time.Millisecond},
			{1 * time.Millisecond, 30 * time.Millisecond},
		}
		for i, d := range delays {
			t0, t1, t2 := exchange(base.Add(time.Duration(i)*time.Second), offset, d.up, d.down)
			c.Add(t0, t1, t2)
			got, rtt, ok := c.Offset()
			if !ok {
				t.Fatalf("offset %v: not ok after %d samples", offset, i+1)
			}
			if err := (got - offset).Abs(); err > rtt/2 {
				t.Errorf("offset %v sample %d: estimate %v, error %v > rtt/2 (%v)", offset, i, got, err, rtt/2)
			}
		}
		got, rtt, _ := c.Offset()
		if rtt != 12*time.Millisecond {
			t.Errorf("offset %v: rtt = %v, want the minimum 12ms", offset, rtt)
		}
		// up 3, down 9: estimate is offset + (3-9)/2 = offset − 3 ms.
		if want := offset - 3*time.Millisecond; got != want {
			t.Errorf("offset %v: estimate %v, want %v (from the min-rtt sample)", offset, got, want)
		}
	}
}

func TestClockSyncKeepsLastSamples(t *testing.T) {
	base := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	var c ClockSync
	// A very good early sample must age out after clockSamples newer ones.
	c.Add(exchange(base, time.Second, time.Millisecond, time.Millisecond))
	for i := 1; i <= clockSamples; i++ {
		c.Add(exchange(base.Add(time.Duration(i)*time.Second), time.Second, 10*time.Millisecond+time.Duration(i)*time.Millisecond, 10*time.Millisecond))
	}
	_, rtt, ok := c.Offset()
	if !ok || rtt != 21*time.Millisecond {
		t.Errorf("rtt = %v ok=%v, want 21ms after the 2 ms sample aged out", rtt, ok)
	}
	// Samples with a negative rtt (wall clock stepped back) are ignored.
	var d ClockSync
	d.Add(base.UnixNano(), base.UnixNano(), base.Add(-time.Millisecond).UnixNano())
	if _, _, ok := d.Offset(); ok {
		t.Error("negative-rtt sample accepted")
	}
}

func TestPeerClockOffset(t *testing.T) {
	host, ctl := connect(t)
	if _, _, ok := host.ClockOffset(); ok {
		t.Error("host reports a clock offset")
	}
	deadline := time.Now().Add(time.Second)
	for {
		off, rtt, ok := ctl.ClockOffset()
		if ok {
			if off.Abs() >= 5*time.Millisecond {
				t.Errorf("loopback offset = %v (rtt %v), want ≈0", off, rtt)
			}
			if rtt < 0 || rtt > time.Second {
				t.Errorf("rtt = %v", rtt)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("clock not synced within 1s")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

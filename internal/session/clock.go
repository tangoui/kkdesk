package session

import (
	"encoding/binary"
	"sync"
	"time"
)

// Clock synchronisation between controller and host, so the controller can
// convert the host's capture timestamps to its own clock. The controller
// sends ping{t0} over the reliable meta channel, the host answers
// pong{t0, t1} with t1 its wall clock on receipt, and the controller, on
// receiving it at t2, estimates rtt = t2−t0 and offset = t1 − (t0+t2)/2
// (host clock − controller clock). The error of one estimate is at most
// rtt/2 (all of it if the path is fully asymmetric), so the sample with the
// smallest rtt among the latest clockSamples is used.
//
// All timestamps are wall-clock Unix nanoseconds: monotonic readings are not
// comparable across machines.
const (
	clockSamples    = 10
	clockQuickPings = 3 // at connect, clockQuickGap apart
	clockQuickGap   = 100 * time.Millisecond
	clockInterval   = time.Second

	metaPing byte = 'p' // 'p' i64 t0 (controller Unix nanos, big endian)
	metaPong byte = 'o' // 'o' i64 t0, i64 t1 (host Unix nanos at receipt)
)

// ClockSync estimates a remote clock's offset from ping/pong samples. The
// zero value is ready to use; it is safe for concurrent use.
type ClockSync struct {
	mu      sync.Mutex
	samples [clockSamples]clockSample
	n, next int
}

type clockSample struct{ offset, rtt time.Duration }

// Add records one exchange: t0 local send time, t1 remote receive time, t2
// local receive time (Unix nanoseconds). Samples with a negative round trip
// (the local wall clock stepped back) are ignored.
func (c *ClockSync) Add(t0, t1, t2 int64) {
	rtt := t2 - t0
	if rtt < 0 {
		return
	}
	// t1 − (t0+t2)/2 without overflowing int64 on the sum.
	off := t1 - t0 - rtt/2
	c.mu.Lock()
	c.samples[c.next] = clockSample{time.Duration(off), time.Duration(rtt)}
	c.next = (c.next + 1) % clockSamples
	c.n = min(c.n+1, clockSamples)
	c.mu.Unlock()
}

// Offset returns remote clock − local clock and the round trip of the sample
// it came from (the smallest among the latest ones). ok is false before the
// first sample.
func (c *ClockSync) Offset() (offset, rtt time.Duration, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.n == 0 {
		return 0, 0, false
	}
	best := c.samples[0]
	for _, s := range c.samples[1:c.n] {
		if s.rtt < best.rtt {
			best = s
		}
	}
	return best.offset, best.rtt, true
}

// ClockOffset returns the host's clock minus this controller's clock and
// the round trip of the sample it is based on. A host timestamp converts to
// the controller's clock as hostTime − offset. ok is false until a ping has
// been answered, and always on the host side.
func (p *Peer) ClockOffset() (offset, rtt time.Duration, ok bool) {
	if p.isHost {
		return 0, 0, false
	}
	return p.clock.Offset()
}

// clockLoop pings the host until the peer is done (controller): a few quick
// pings right after connecting, then one per clockInterval.
func (p *Peer) clockLoop() {
	t := time.NewTimer(0)
	defer t.Stop()
	for i := 0; ; i++ {
		select {
		case <-p.done:
			return
		case <-t.C:
		}
		p.sendClock(metaPing, time.Now().UnixNano(), 0)
		if i < clockQuickPings-1 {
			t.Reset(clockQuickGap)
		} else {
			t.Reset(clockInterval)
		}
	}
}

func (p *Peer) sendClock(kind byte, t0, t1 int64) {
	p.mu.Lock()
	dc := p.dcMeta
	p.mu.Unlock()
	if dc == nil {
		return
	}
	b := make([]byte, 9, 17)
	b[0] = kind
	binary.BigEndian.PutUint64(b[1:], uint64(t0))
	if kind == metaPong {
		b = binary.BigEndian.AppendUint64(b, uint64(t1))
	}
	_ = dc.Send(b)
}

// handleClock answers pings (host) and records pongs (controller).
func (p *Peer) handleClock(b []byte) {
	now := time.Now().UnixNano()
	switch {
	case b[0] == metaPing && len(b) == 9 && p.isHost:
		p.sendClock(metaPong, int64(binary.BigEndian.Uint64(b[1:])), now)
	case b[0] == metaPong && len(b) == 17 && !p.isHost:
		p.clock.Add(int64(binary.BigEndian.Uint64(b[1:])), int64(binary.BigEndian.Uint64(b[9:])), now)
	}
}

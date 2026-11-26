package app

import "sync"

// handshakeLimiter bounds the LAN handshakes running at once, overall and
// per remote address, so stalled connections from one address cannot hold
// up everyone else's.
type handshakeLimiter struct {
	mu       sync.Mutex
	total    int
	perAddr  map[string]int
	max      int
	maxPerIP int
}

func newHandshakeLimiter(max, maxPerIP int) *handshakeLimiter {
	return &handshakeLimiter{perAddr: map[string]int{}, max: max, maxPerIP: maxPerIP}
}

// acquire takes a slot for addr; ok is false when the limits are reached.
// release frees it and may be called more than once.
func (l *handshakeLimiter) acquire(addr string) (release func(), ok bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.total >= l.max || l.perAddr[addr] >= l.maxPerIP {
		return nil, false
	}
	l.total++
	l.perAddr[addr]++
	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			defer l.mu.Unlock()
			l.total--
			if l.perAddr[addr]--; l.perAddr[addr] <= 0 {
				delete(l.perAddr, addr)
			}
		})
	}, true
}

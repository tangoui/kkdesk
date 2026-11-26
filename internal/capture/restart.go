package capture

import (
	"log"
	"sync"
	"sync/atomic"
	"time"
)

// restarter re-opens a capture stream that the system stopped (macOS stops
// ScreenCaptureKit streams on its own, e.g. around a screenshot), off the
// callback queue, retrying until it succeeds or the session closes.
type restarter struct {
	// delays are the waits before each attempt; the last one repeats.
	delays []time.Duration
	// reopen stops the old stream and starts a new one.
	reopen func() error

	mu      sync.Mutex
	running bool
	closed  bool
	wake    chan struct{} // closed by close to cut a wait short
	wg      sync.WaitGroup
	count   atomic.Uint64
}

// trigger starts a restart unless one is running or the session closed.
func (r *restarter) trigger(reason string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || r.running {
		return
	}
	if r.wake == nil {
		r.wake = make(chan struct{})
	}
	r.running = true
	r.wg.Add(1)
	log.Printf("kkdesk capture: stream stopped (%s); restarting", reason)
	go r.run(r.wake)
}

func (r *restarter) run(wake chan struct{}) {
	defer r.wg.Done()
	defer func() {
		r.mu.Lock()
		r.running = false
		r.mu.Unlock()
	}()
	for i := 0; ; i++ {
		d := r.delays[min(i, len(r.delays)-1)]
		select {
		case <-wake:
			return
		case <-time.After(d):
		}
		if r.isClosed() {
			return
		}
		err := r.reopen()
		if err == nil {
			r.count.Add(1)
			log.Printf("kkdesk capture: stream restarted after %d attempt(s)", i+1)
			return
		}
		if i < 5 || i%30 == 0 {
			log.Printf("kkdesk capture: restart attempt %d: %v", i+1, err)
		}
	}
}

func (r *restarter) isClosed() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.closed
}

// reopened returns how many restarts succeeded.
func (r *restarter) reopened() uint64 { return r.count.Load() }

// close stops further restarts and waits for a running one to return.
func (r *restarter) close() {
	r.mu.Lock()
	if !r.closed {
		r.closed = true
		if r.wake != nil {
			close(r.wake)
		}
	}
	r.mu.Unlock()
	r.wg.Wait()
}

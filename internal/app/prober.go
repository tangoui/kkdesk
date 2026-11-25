package app

import (
	"context"
	"net"
	"slices"
	"sync"
	"time"
)

// ProbeStatus is the online state of an address book entry.
type ProbeStatus int

const (
	ProbeUnknown ProbeStatus = iota // not probed yet (探测中)
	ProbeOnline                     // the port accepted a TCP connection
	ProbeOffline                    // refused, unreachable or timed out
)

// Prober defaults: every 10 s, 800 ms per attempt, 8 at a time.
const (
	ProbeInterval = 10 * time.Second
	ProbeTimeout  = 800 * time.Millisecond
	ProbeLimit    = 8
)

// ProberConfig configures a Prober; zero fields take the defaults.
type ProberConfig struct {
	Interval time.Duration
	Timeout  time.Duration
	Limit    int // probes at once
	// Dial tries one address (tests); nil opens and closes a TCP
	// connection. It must return when ctx ends.
	Dial func(ctx context.Context, addr string) error
}

// Prober checks in the background whether the hosts of 我的设备 listen:
// a round every Interval, each address a TCP connect bounded by Timeout,
// at most Limit at once. It never blocks the caller. Close stops it.
type Prober struct {
	cfg    ProberConfig
	notify func()
	ctx    context.Context
	cancel context.CancelFunc
	kick   chan struct{}
	wg     sync.WaitGroup

	mu      sync.Mutex
	targets []string
	status  map[string]ProbeStatus
	closed  bool
}

// NewProber starts a prober with no targets. notify is called (from any
// goroutine) when a status changes.
func NewProber(cfg ProberConfig, notify func()) *Prober {
	if cfg.Interval <= 0 {
		cfg.Interval = ProbeInterval
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = ProbeTimeout
	}
	if cfg.Limit <= 0 {
		cfg.Limit = ProbeLimit
	}
	if cfg.Dial == nil {
		cfg.Dial = dialTCP
	}
	if notify == nil {
		notify = func() {}
	}
	ctx, cancel := context.WithCancel(context.Background())
	p := &Prober{cfg: cfg, notify: notify, ctx: ctx, cancel: cancel, kick: make(chan struct{}, 1), status: map[string]ProbeStatus{}}
	p.wg.Add(1)
	go p.loop()
	return p
}

func dialTCP(ctx context.Context, addr string) error {
	var d net.Dialer
	c, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	return c.Close()
}

// SetTargets replaces the addresses to probe. New addresses are probed at
// once; removed ones are forgotten.
func (p *Prober) SetTargets(addrs []string) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	addrs = slices.Compact(slices.Sorted(slices.Values(addrs)))
	fresh := false
	for _, a := range addrs {
		if _, ok := p.status[a]; !ok {
			p.status[a] = ProbeUnknown
			fresh = true
		}
	}
	for a := range p.status {
		if !slices.Contains(addrs, a) {
			delete(p.status, a)
		}
	}
	p.targets = addrs
	p.mu.Unlock()
	if fresh {
		p.ProbeNow()
	}
}

// ProbeNow starts a round now (unless one is running).
func (p *Prober) ProbeNow() {
	select {
	case p.kick <- struct{}{}:
	default:
	}
}

// Status returns the state of addr.
func (p *Prober) Status(addr string) ProbeStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.status[addr]
}

// Close stops probing and waits for running probes, which are cancelled.
func (p *Prober) Close() {
	p.mu.Lock()
	p.closed = true
	p.mu.Unlock()
	p.cancel()
	p.wg.Wait()
}

func (p *Prober) loop() {
	defer p.wg.Done()
	t := time.NewTimer(p.cfg.Interval)
	defer t.Stop()
	for {
		select {
		case <-p.ctx.Done():
			return
		case <-t.C:
		case <-p.kick:
		}
		p.round()
		t.Reset(p.cfg.Interval)
	}
}

// round probes every target, Limit at a time.
func (p *Prober) round() {
	p.mu.Lock()
	targets := slices.Clone(p.targets)
	p.mu.Unlock()
	sem := make(chan struct{}, p.cfg.Limit)
	var wg sync.WaitGroup
	for _, a := range targets {
		select {
		case sem <- struct{}{}:
		case <-p.ctx.Done():
			wg.Wait()
			return
		}
		wg.Add(1)
		go func() {
			defer func() { <-sem; wg.Done() }()
			ctx, cancel := context.WithTimeout(p.ctx, p.cfg.Timeout)
			err := p.cfg.Dial(ctx, a)
			cancel()
			if p.ctx.Err() != nil {
				return
			}
			st := ProbeOnline
			if err != nil {
				st = ProbeOffline
			}
			p.mu.Lock()
			old, known := p.status[a]
			if known {
				p.status[a] = st
			}
			p.mu.Unlock()
			if known && old != st {
				p.notify()
			}
		}()
	}
	wg.Wait()
}

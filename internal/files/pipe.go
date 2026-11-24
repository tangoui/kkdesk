package files

import (
	"errors"
	"io"
	"sync"
	"time"
)

// Pipe returns two connected in-memory Channels. Each direction queues sent
// messages and delivers them in order on its own goroutine; BufferedAmount
// counts bytes not yet delivered, so flow control behaves as on a real data
// channel. Messages larger than a data channel accepts are rejected. Close
// on either end closes both.
func Pipe() (*PipeEnd, *PipeEnd) {
	s := &pipeShared{done: make(chan struct{})}
	a := &PipeEnd{shared: s}
	b := &PipeEnd{shared: s}
	a.peer, b.peer = b, a
	for _, e := range []*PipeEnd{a, b} {
		e.cond = sync.NewCond(&e.mu)
		go e.deliver()
	}
	return a, b
}

type pipeShared struct {
	once sync.Once
	done chan struct{}
}

type pipeMsg struct {
	text bool
	data []byte
}

// PipeEnd is one end of a Pipe.
type PipeEnd struct {
	peer   *PipeEnd
	shared *pipeShared

	mu        sync.Mutex
	cond      *sync.Cond
	queue     []pipeMsg // sent by this end, not yet delivered to peer
	buffered  uint64
	threshold uint64
	onLow     func()
	onClose   func()
	closeOnce sync.Once

	hmu   sync.Mutex // guards onMsg (leaf lock)
	onMsg func(bool, []byte)

	// test hooks, set before use
	delay  time.Duration               // per delivered message
	tamper func(isText bool, b []byte) // may modify messages sent from this end
}

var _ Channel = (*PipeEnd)(nil)

func (e *PipeEnd) closed() bool {
	select {
	case <-e.shared.done:
		return true
	default:
		return false
	}
}

func (e *PipeEnd) push(text bool, b []byte) error {
	if len(b) > maxMessage {
		return errors.New("files: pipe: message too large")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed() {
		return io.ErrClosedPipe
	}
	e.queue = append(e.queue, pipeMsg{text, append([]byte(nil), b...)})
	e.buffered += uint64(len(b))
	e.cond.Signal()
	return nil
}

// SendText implements Channel.
func (e *PipeEnd) SendText(s string) error { return e.push(true, []byte(s)) }

// Send implements Channel.
func (e *PipeEnd) Send(b []byte) error { return e.push(false, b) }

// OnMessage implements Channel. Messages wait until a handler is set.
func (e *PipeEnd) OnMessage(f func(bool, []byte)) {
	e.hmu.Lock()
	e.onMsg = f
	e.hmu.Unlock()
	p := e.peer
	p.mu.Lock()
	p.cond.Broadcast()
	p.mu.Unlock()
}

func (e *PipeEnd) handler() func(bool, []byte) {
	e.hmu.Lock()
	defer e.hmu.Unlock()
	return e.onMsg
}

// BufferedAmount implements Channel.
func (e *PipeEnd) BufferedAmount() uint64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.buffered
}

// SetBufferedAmountLowThreshold implements Channel.
func (e *PipeEnd) SetBufferedAmountLowThreshold(n uint64) {
	e.mu.Lock()
	e.threshold = n
	e.mu.Unlock()
}

// OnBufferedAmountLow implements Channel.
func (e *PipeEnd) OnBufferedAmountLow(f func()) {
	e.mu.Lock()
	e.onLow = f
	e.mu.Unlock()
}

// OnClose implements Channel.
func (e *PipeEnd) OnClose(f func()) {
	e.mu.Lock()
	e.onClose = f
	e.mu.Unlock()
	if e.closed() {
		e.fireClose()
	}
}

// Close closes both ends; queued messages are dropped.
func (e *PipeEnd) Close() error {
	e.shared.once.Do(func() { close(e.shared.done) })
	for _, x := range []*PipeEnd{e, e.peer} {
		x.mu.Lock()
		x.cond.Broadcast()
		x.mu.Unlock()
		go x.fireClose()
	}
	return nil
}

func (e *PipeEnd) fireClose() {
	e.mu.Lock()
	f := e.onClose
	e.mu.Unlock()
	if f != nil {
		e.closeOnce.Do(f)
	}
}

// deliver hands this end's queued messages to the peer's handler.
func (e *PipeEnd) deliver() {
	p := e.peer
	for {
		e.mu.Lock()
		for len(e.queue) == 0 && !e.closed() {
			e.cond.Wait()
		}
		if e.closed() {
			e.mu.Unlock()
			return
		}
		m := e.queue[0]
		e.queue[0] = pipeMsg{}
		e.queue = e.queue[1:]
		tamper := e.tamper
		e.mu.Unlock()

		if e.delay > 0 {
			time.Sleep(e.delay)
		}
		if tamper != nil {
			tamper(m.text, m.data)
		}
		// Wait for the receiving side to register its handler.
		e.mu.Lock()
		h := p.handler()
		for h == nil && !e.closed() {
			e.cond.Wait()
			h = p.handler()
		}
		e.mu.Unlock()
		if e.closed() {
			return
		}
		h(m.text, m.data)

		e.mu.Lock()
		before := e.buffered
		e.buffered -= uint64(len(m.data))
		var low func()
		if before > e.threshold && e.buffered <= e.threshold {
			low = e.onLow
		}
		e.mu.Unlock()
		if low != nil {
			low()
		}
	}
}

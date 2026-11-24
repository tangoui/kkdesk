package files

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"time"
)

// Channel is the data channel the engine runs on: ordered, reliable and
// message based. *webrtc.DataChannel fits with a thin adapter (see
// internal/session); Pipe is an in-memory implementation for tests.
//
// OnMessage callbacks must be delivered one at a time, in order; the engine
// may block in them briefly (it writes received chunks to disk there), which
// applies back pressure to the sender. data is owned by the callee.
type Channel interface {
	SendText(s string) error
	Send(b []byte) error
	OnMessage(f func(isText bool, data []byte))
	BufferedAmount() uint64
	SetBufferedAmountLowThreshold(n uint64)
	OnBufferedAmountLow(f func())
	OnClose(f func())
}

// endpoint wraps a Channel with JSON framing, fragment reassembly and flow
// control. Both Client and Server use one.
type endpoint struct {
	ch Channel

	textMu sync.Mutex // keeps a fragmented JSON message contiguous
	frag   []byte     // reassembly buffer; message goroutine only

	flowMu sync.Mutex
	lowCh  chan struct{} // closed and replaced on each low-water event

	closed    chan struct{}
	closeOnce sync.Once

	onJSON  func(m *msg)
	onData  func(transfer uint64, off int64, data []byte)
	onClose func()
}

func newEndpoint(ch Channel, onJSON func(*msg), onData func(uint64, int64, []byte), onClose func()) *endpoint {
	e := &endpoint{ch: ch, lowCh: make(chan struct{}), closed: make(chan struct{}), onJSON: onJSON, onData: onData, onClose: onClose}
	ch.SetBufferedAmountLowThreshold(lowWater)
	ch.OnBufferedAmountLow(e.low)
	return e
}

// start registers the message and close handlers. It is separate from
// newEndpoint because a channel may deliver queued messages from inside
// OnMessage, and the owner must be fully set up by then.
func (e *endpoint) start() {
	e.ch.OnClose(e.close)
	e.ch.OnMessage(e.recv)
}

func (e *endpoint) low() {
	e.flowMu.Lock()
	close(e.lowCh)
	e.lowCh = make(chan struct{})
	e.flowMu.Unlock()
}

func (e *endpoint) close() {
	e.closeOnce.Do(func() {
		close(e.closed)
		if e.onClose != nil {
			e.onClose()
		}
	})
}

func (e *endpoint) isClosed() bool {
	select {
	case <-e.closed:
		return true
	default:
		return false
	}
}

func (e *endpoint) recv(isText bool, data []byte) {
	if isText {
		b := data
		if len(e.frag) > 0 {
			b = append(e.frag, data...)
			e.frag = nil
		}
		var m msg
		if json.Unmarshal(b, &m) == nil {
			e.onJSON(&m)
		}
		return
	}
	kind, transfer, off, payload, err := parseHeader(data)
	if err != nil {
		return
	}
	switch kind {
	case kindData:
		e.onData(transfer, off, payload)
	case kindJSON:
		if len(e.frag)+len(payload) > maxJSON {
			e.frag = nil
			return
		}
		e.frag = append(e.frag, payload...)
	}
}

// send writes one JSON message, fragmenting it if needed. Control messages
// do not wait for flow control; they are small and must not be starved.
func (e *endpoint) send(m msg) error {
	if e.isClosed() {
		return ErrClosed
	}
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	e.textMu.Lock()
	defer e.textMu.Unlock()
	for len(b) > maxText {
		part := make([]byte, headerLen+ChunkSize)
		putHeader(part, kindJSON, 0, 0)
		n := copy(part[headerLen:], b)
		if err := e.ch.Send(part[:headerLen+n]); err != nil {
			return e.sendErr(err)
		}
		b = b[n:]
	}
	return e.sendErr(e.ch.SendText(string(b)))
}

// sendErr maps a send failure on a closing channel to ErrClosed; the close
// notification may arrive after the first failed send.
func (e *endpoint) sendErr(err error) error {
	if err != nil && (e.isClosed() || errors.Is(err, io.ErrClosedPipe)) {
		return ErrClosed
	}
	return err
}

// waitFlow blocks while more than highWater bytes are queued on the channel.
func (e *endpoint) waitFlow(ctx context.Context) error {
	for e.ch.BufferedAmount() > highWater {
		e.flowMu.Lock()
		c := e.lowCh
		e.flowMu.Unlock()
		if e.ch.BufferedAmount() <= highWater {
			break
		}
		t := time.NewTimer(50 * time.Millisecond) // in case an event is missed
		select {
		case <-c:
		case <-t.C:
		case <-ctx.Done():
			t.Stop()
			return context.Cause(ctx)
		case <-e.closed:
			t.Stop()
			return ErrClosed
		}
		t.Stop()
	}
	return nil
}

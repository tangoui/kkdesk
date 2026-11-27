package session

import (
	"encoding/binary"
	"errors"
	"io"
	"net"
	"sync/atomic"

	"github.com/pion/webrtc/v4"
)

// MaxClipboard bounds the clipboard text sent or accepted (bytes of UTF-8).
const MaxClipboard = 1 << 20

// clipChunkSize is the payload of one clipboard message, well below the
// SCTP message size every WebRTC stack accepts.
const clipChunkSize = 16 << 10

// metaClip carries clipboard text over the meta channel, in chunks:
// 'x' u32 textID, u8 last (1 on the final chunk), payload (big endian).
// Peers that predate it ignore the unknown type.
const metaClip byte = 'x'

// ErrClipboardTooLong is returned by SendClipboard for text over
// MaxClipboard.
var ErrClipboardTooLong = errors.New("session: clipboard text too long")

var clipIDs atomic.Uint32

// SendClipboard sends clipboard text to the other side (either role).
func (p *Peer) SendClipboard(text string) error {
	if len(text) > MaxClipboard {
		return ErrClipboardTooLong
	}
	select {
	case <-p.Done():
		return io.ErrClosedPipe
	default:
	}
	p.mu.Lock()
	dc := p.dcMeta
	p.mu.Unlock()
	if dc == nil || dc.ReadyState() == webrtc.DataChannelStateConnecting {
		return errors.New("session: meta channel not open")
	}
	id := clipIDs.Add(1)
	b := []byte(text)
	for {
		n := min(len(b), clipChunkSize)
		last := n == len(b)
		if err := dc.Send(clipChunk(id, last, b[:n])); err != nil {
			// Pion delivers OnClose from its receive loop. A failed
			// write can see the closed transport before that loop exits.
			if errors.Is(err, io.ErrClosedPipe) || errors.Is(err, net.ErrClosed) {
				p.shutdown()
			}
			return err
		}
		if last {
			return nil
		}
		b = b[n:]
	}
}

// OnClipboard sets the callback for clipboard text from the other side.
// Text that arrives while no callback is set is dropped: the host sets it
// only once the connection is accepted.
func (p *Peer) OnClipboard(f func(string)) {
	p.mu.Lock()
	p.onClipboard = f
	p.mu.Unlock()
}

func clipChunk(id uint32, last bool, payload []byte) []byte {
	b := make([]byte, 6+len(payload))
	b[0] = metaClip
	binary.BigEndian.PutUint32(b[1:], id)
	if last {
		b[5] = 1
	}
	copy(b[6:], payload)
	return b
}

// clipAssembler joins the chunks of one text. Messages of the meta channel
// arrive in order, one at a time.
type clipAssembler struct {
	id      uint32
	open    bool
	buf     []byte
	dropped bool // the text id went over MaxClipboard: skip it to its last chunk
}

// add takes one metaClip message and returns the text once it is complete.
func (a *clipAssembler) add(m []byte) (string, bool) {
	if len(m) < 6 {
		return "", false
	}
	id, last := binary.BigEndian.Uint32(m[1:]), m[5] == 1
	if a.dropped && id == a.id {
		if last {
			a.dropped = false
		}
		return "", false
	}
	if !a.open || id != a.id {
		a.id, a.open, a.dropped, a.buf = id, true, false, a.buf[:0] // a new text drops an unfinished one
	}
	if len(a.buf)+len(m)-6 > MaxClipboard {
		a.open, a.buf, a.dropped = false, nil, !last
		return "", false
	}
	a.buf = append(a.buf, m[6:]...)
	if !last {
		return "", false
	}
	s := string(a.buf)
	a.open, a.buf = false, a.buf[:0]
	return s, true
}

func (p *Peer) handleClip(m []byte) {
	s, ok := p.clip.add(m)
	if !ok {
		return
	}
	p.mu.Lock()
	f := p.onClipboard
	p.mu.Unlock()
	if f != nil {
		f(s)
	}
}

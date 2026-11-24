package session

import (
	"encoding/binary"
	"errors"
)

// Quality is the controller's choice of video quality, sent to the host over
// the reliable meta data channel.
type Quality struct {
	// BitrateBps is the target bitrate; 0 means automatic.
	BitrateBps int
	// FPS is the target frame rate; 0 leaves it unchanged.
	FPS int
}

// SendQuality asks the host to change its bitrate and frame rate
// (controller). Values are clamped to the wire format (u32 bps, u16 fps).
func (p *Peer) SendQuality(q Quality) error {
	p.mu.Lock()
	dc := p.dcMeta
	p.mu.Unlock()
	if dc == nil {
		return errors.New("session: meta channel not open")
	}
	b := make([]byte, 7)
	b[0] = metaQuality
	binary.BigEndian.PutUint32(b[1:], uint32(max(0, min(q.BitrateBps, 1<<32-1))))
	binary.BigEndian.PutUint16(b[5:], uint16(max(0, min(q.FPS, 1<<16-1))))
	return dc.Send(b)
}

// OnQuality sets the callback for quality requests from the controller
// (host).
func (p *Peer) OnQuality(f func(Quality)) {
	p.mu.Lock()
	p.onQuality = f
	p.mu.Unlock()
}

func (p *Peer) handleQuality(b []byte) {
	if len(b) != 7 || !p.isHost {
		return
	}
	q := Quality{BitrateBps: int(binary.BigEndian.Uint32(b[1:])), FPS: int(binary.BigEndian.Uint16(b[5:]))}
	p.mu.Lock()
	f := p.onQuality
	p.mu.Unlock()
	if f != nil {
		f(q)
	}
}

// Reason returns the reason the other side gave when it closed the session
// (CloseWithReason), or "" if it gave none or has not closed.
func (p *Peer) Reason() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.reason
}

// RemoteAddr returns the other side's signaling address (host:port).
func (p *Peer) RemoteAddr() string { return p.remote }

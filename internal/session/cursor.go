package session

import (
	"errors"

	"github.com/pion/webrtc/v4"
)

const metaCursor byte = 0x9b

// CursorShape is the host cursor's one-byte wire code.
type CursorShape byte

const (
	CursorDefault    CursorShape = 0
	CursorPointer    CursorShape = 1
	CursorText       CursorShape = 2
	CursorNotAllowed CursorShape = 3
	CursorResizeEW   CursorShape = 4
	CursorResizeNS   CursorShape = 5
	CursorMove       CursorShape = 6
	CursorGrab       CursorShape = 7
	CursorGrabbing   CursorShape = 8
)

// SendCursor publishes the host cursor after authentication and admission.
// The host starts publishing only when it begins serving the desktop.
func (p *Peer) SendCursor(shape CursorShape) error {
	if !p.isHost || p.mode == ModeFiles || p.passwordPending() {
		return errors.New("session: cannot send host cursor")
	}
	return p.sendMeta([]byte{metaCursor, byte(shape)})
}

func (p *Peer) handleCursor(msg webrtc.DataChannelMessage) {
	if p.isHost || p.mode == ModeFiles || msg.IsString || len(msg.Data) != 2 {
		return
	}
	shape := CursorShape(msg.Data[1])
	if shape > CursorGrabbing {
		shape = CursorDefault
	}
	p.mu.Lock()
	changed := p.cursor != shape
	p.cursor = shape
	notify := p.onCursorChange
	p.mu.Unlock()
	if changed && notify != nil {
		notify()
	}
}

// Cursor returns the latest host cursor. The initial shape is the arrow.
func (p *Peer) Cursor() CursorShape {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.cursor
}

// OnCursorChange installs a callback for subsequent changes to Cursor.
func (p *Peer) OnCursorChange(notify func()) {
	p.mu.Lock()
	p.onCursorChange = notify
	p.mu.Unlock()
}

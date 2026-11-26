package session

import (
	"errors"

	"github.com/pion/webrtc/v4"
)

// 'a' followed by 0 (denied) or 1 (granted) on the reliable meta channel.
// Older peers ignore this message. No message leaves input enabled.
const metaAccessibility byte = 'a'

func encodeAccessibility(granted bool) []byte {
	status := byte(0)
	if granted {
		status = 1
	}
	return []byte{metaAccessibility, status}
}

func decodeAccessibility(b []byte) (granted, ok bool) {
	if len(b) != 2 || b[0] != metaAccessibility || b[1] > 1 {
		return false, false
	}
	return b[1] == 1, true
}

// SendAccessibility publishes the host's current macOS input permission.
func (p *Peer) SendAccessibility(granted bool) error {
	p.mu.Lock()
	dc := p.dcMeta
	p.mu.Unlock()
	if !p.isHost || p.mode == ModeFiles || dc == nil {
		return errors.New("session: cannot send host accessibility status")
	}
	return dc.Send(encodeAccessibility(granted))
}

func (p *Peer) handleAccessibility(msg webrtc.DataChannelMessage) {
	if p.isHost || p.mode == ModeFiles || msg.IsString {
		return
	}
	granted, ok := decodeAccessibility(msg.Data)
	if !ok {
		return
	}
	p.mu.Lock()
	changed := p.accessibilityDenied != !granted
	p.accessibilityDenied = !granted
	notify := p.onAccessibilityChange
	p.mu.Unlock()
	if changed && notify != nil {
		notify()
	}
}

// AccessibilityDenied reports whether the host explicitly denied input.
func (p *Peer) AccessibilityDenied() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.accessibilityDenied
}

// OnAccessibilityChange installs a callback for changes to AccessibilityDenied.
func (p *Peer) OnAccessibilityChange(notify func()) {
	p.mu.Lock()
	p.onAccessibilityChange = notify
	p.mu.Unlock()
}

package session

import (
	"bytes"
	"errors"
	"unicode/utf8"

	"github.com/pion/webrtc/v4"
)

const (
	metaLockScreen byte = 0x98 + iota
	metaUnlockRequest
	metaUnlockResult
)

const maxUnlockBytes = 1024

type LockScreenState byte

const (
	LockScreenUnknown LockScreenState = iota
	LockScreenUnlocked
	LockScreenReady
	LockScreenComponentUnavailable
	LockScreenPlatformUnsupported
	LockScreenSignatureRequired
	LockScreenWrongInstallLocation
	LockScreenServiceUnavailable
	LockScreenComponentNeedsUpdate
	LockScreenComponentError
	LockScreenApprovalRequired
)

type UnlockResult byte

const (
	UnlockSucceeded UnlockResult = iota + 1
	UnlockStillLocked
	UnlockUnsupported
	UnlockTooMany
)

var errLockScreenMessage = errors.New("session: invalid lock screen message")

func (p *Peer) LockScreen() LockScreenState {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lockScreen
}

// OnLockScreenChange also notifies the caller if a state has already arrived.
func (p *Peer) OnLockScreenChange(fn func()) {
	p.mu.Lock()
	p.onLockScreenChange = fn
	known := p.lockScreen != LockScreenUnknown
	p.mu.Unlock()
	if known && fn != nil {
		fn()
	}
}

// OnUnlock admits requests to the host's active desktop session. The password
// belongs to the callback for its duration. The transport clears it on return.
func (p *Peer) OnUnlock(fn func([]byte)) {
	p.mu.Lock()
	p.onUnlock = fn
	p.mu.Unlock()
}

func (p *Peer) OnUnlockResult(fn func(UnlockResult)) {
	p.mu.Lock()
	p.onUnlockResult = fn
	p.mu.Unlock()
}

func (p *Peer) SendLockScreen(state LockScreenState) error {
	if !p.isHost || !p.canSendLockScreen() || state < LockScreenUnlocked || state > LockScreenApprovalRequired {
		return errLockScreenMessage
	}
	return p.sendMeta([]byte{metaLockScreen, byte(state)})
}

func (p *Peer) SendUnlock(password []byte) error {
	if p.isHost || !p.canSendLockScreen() || !validUnlockSecret(password) {
		return errLockScreenMessage
	}
	// The data channel copies its payload when Send packetizes it.
	frame := append([]byte{metaUnlockRequest}, password...)
	defer clear(frame[1:])
	return p.sendMeta(frame)
}

func (p *Peer) SendUnlockResult(result UnlockResult) error {
	if !p.isHost || !p.canSendLockScreen() || result < UnlockSucceeded || result > UnlockTooMany {
		return errLockScreenMessage
	}
	return p.sendMeta([]byte{metaUnlockResult, byte(result)})
}

func (p *Peer) canSendLockScreen() bool {
	select {
	case <-p.done:
		return false
	default:
		return p.mode != ModeFiles && !p.passwordPending()
	}
}

func validUnlockSecret(password []byte) bool {
	return len(password) > 0 && len(password) <= maxUnlockBytes &&
		utf8.Valid(password) && bytes.IndexByte(password, 0) < 0
}

func (p *Peer) handleLockScreen(msg webrtc.DataChannelMessage) {
	b := msg.Data
	if msg.IsString || !p.canSendLockScreen() {
		return
	}
	switch b[0] {
	case metaLockScreen:
		if p.isHost || len(b) != 2 {
			return
		}
		state := LockScreenState(b[1])
		if state < LockScreenUnlocked || state > LockScreenApprovalRequired {
			return
		}
		p.mu.Lock()
		changed := p.lockScreen != state
		p.lockScreen = state
		fn := p.onLockScreenChange
		p.mu.Unlock()
		if changed && fn != nil {
			fn()
		}
	case metaUnlockRequest:
		if !p.isHost || !validUnlockSecret(b[1:]) {
			return
		}
		p.mu.Lock()
		fn := p.onUnlock
		p.mu.Unlock()
		if fn != nil {
			fn(b[1:])
		}
	case metaUnlockResult:
		if p.isHost || len(b) != 2 {
			return
		}
		result := UnlockResult(b[1])
		if result < UnlockSucceeded || result > UnlockTooMany {
			return
		}
		p.mu.Lock()
		fn := p.onUnlockResult
		p.mu.Unlock()
		if fn != nil {
			fn(result)
		}
	}
}

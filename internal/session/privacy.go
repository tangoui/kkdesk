package session

import (
	"errors"
	"unicode/utf8"

	"github.com/pion/webrtc/v4"
)

type PrivacyKind byte

const (
	PrivacyRequest PrivacyKind = 0x90 + iota
	PrivacyState
	LockOnDisconnectRequest
	LockOnDisconnectState
	LockNowRequest
	LockNowResult
)

type PrivacyStatus byte

const (
	PrivacyOff PrivacyStatus = iota
	PrivacyOn
	PrivacyWaiting
	PrivacyDenied
	PrivacyNotAllowed
	PrivacyUnsupported
	PrivacyLocalExit
	PrivacyFailed
)

type PrivacyMessage struct {
	Kind   PrivacyKind
	Status PrivacyStatus
	Detail string
}

func (m PrivacyMessage) isRequest() bool {
	switch m.Kind {
	case PrivacyRequest, LockOnDisconnectRequest, LockNowRequest:
		return true
	}
	return false
}

func (m PrivacyMessage) valid() bool {
	if len(m.Detail) > 2048 || !utf8.ValidString(m.Detail) {
		return false
	}
	if m.Detail != "" && m.Status != PrivacyUnsupported && m.Status != PrivacyFailed {
		return false
	}
	switch m.Kind {
	case PrivacyRequest, LockOnDisconnectRequest:
		return m.Status <= PrivacyOn
	case LockNowRequest:
		return m.Status == PrivacyOff
	case PrivacyState:
		return m.Status <= PrivacyLocalExit
	case LockOnDisconnectState:
		return m.Status <= PrivacyOn || m.Status == PrivacyUnsupported
	case LockNowResult:
		return m.Status == PrivacyOn || m.Status == PrivacyUnsupported || m.Status == PrivacyFailed
	}
	return false
}

// OnPrivacy installs the receiver. The host registers it after admitting
// the session so requests made during connection confirmation are dropped.
func (p *Peer) OnPrivacy(fn func(PrivacyMessage)) {
	p.mu.Lock()
	p.onPrivacy = fn
	p.mu.Unlock()
}

func (p *Peer) SendPrivacy(m PrivacyMessage) error {
	if m.isRequest() == p.isHost || !m.valid() || p.passwordPending() {
		return errors.New("session: invalid privacy message")
	}
	frame := []byte{byte(m.Kind)}
	if m.Kind != LockNowRequest {
		frame = append(frame, byte(m.Status))
		frame = append(frame, m.Detail...)
	}
	return p.sendMeta(frame)
}

func (p *Peer) handlePrivacy(msg webrtc.DataChannelMessage) {
	p.mu.Lock()
	fn := p.onPrivacy
	p.mu.Unlock()
	if fn == nil || msg.IsString {
		return
	}
	b := msg.Data
	m := PrivacyMessage{Kind: PrivacyKind(b[0])}
	if m.Kind == LockNowRequest {
		if len(b) != 1 {
			return
		}
	} else {
		if len(b) < 2 || len(b) > 2050 {
			return
		}
		m.Status, m.Detail = PrivacyStatus(b[1]), string(b[2:])
	}
	if m.isRequest() != p.isHost || !m.valid() {
		return
	}
	if p.isHost && p.mode == ModeFiles {
		replies := map[PrivacyKind]PrivacyKind{
			PrivacyRequest: PrivacyState, LockOnDisconnectRequest: LockOnDisconnectState, LockNowRequest: LockNowResult,
		}
		_ = p.SendPrivacy(PrivacyMessage{Kind: replies[m.Kind], Status: PrivacyUnsupported, Detail: "仅传文件会话不支持隐私屏或锁屏"})
		return
	}
	fn(m)
}

// ReceivedBye reports a graceful remote close, including an empty reason.
func (p *Peer) ReceivedBye() bool {
	return p.receivedBye.Load()
}

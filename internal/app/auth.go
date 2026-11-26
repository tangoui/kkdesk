package app

import (
	"fmt"
	"time"

	"github.com/tangoui/kkdesk/internal/session"
)

func identityError(err error) error {
	return fmt.Errorf("无法读取本机设备身份：%w", err)
}

func (h *Host) verifyPassword(transcript session.PasswordTranscript, proof []byte) (string, time.Duration) {
	h.mu.Lock()
	now := time.Now()
	matched, remaining := h.passwordGate.Check(now, transcript, proof, h.password, h.cfg.Fixed)
	locked := !h.closed && remaining == 0 && h.passwordGate.Remaining(now) > 0
	if locked && h.passwordLockTimer == nil {
		h.passwordLockTimer = time.AfterFunc(time.Second, h.tickPasswordLock)
	}
	h.mu.Unlock()
	if locked {
		h.notify()
	}
	return matched, remaining
}

func (h *Host) tickPasswordLock() {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}
	if h.passwordGate.Remaining(time.Now()) > 0 {
		h.passwordLockTimer.Reset(time.Second)
	} else {
		h.passwordLockTimer = nil
	}
	h.mu.Unlock()
	h.notify()
}

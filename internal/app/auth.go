package app

import (
	"fmt"
	"log"
	"time"

	"github.com/tangoui/kkdesk/internal/session"
)

func identityError(err error) error {
	return fmt.Errorf("无法读取本机设备身份：%w", err)
}

// remoteSource is the password-gate source shared by all cross-network
// requests (their addresses are relays or unknown).
const remoteSource = "remote"

// ViewerTrust remembers controllers whose session was accepted: a
// controller that proves such a key (session.Peer.ViewerKey) has a pause
// source of its own, so strangers failing on a shared source cannot keep it
// out. Unknown keys count as their source like anonymous controllers, so new
// keys give an attacker no extra guesses, and a trusted key that fails loses
// its trust, so knowing an old password buys one extra guess per key at
// most.
type ViewerTrust interface {
	ViewerTrusted(key string) bool
	TrustViewer(key string) error
	DistrustViewer(key string) error
}

func (h *Host) verifyPassword(source, viewerKey string, transcript session.PasswordTranscript, proof []byte) (string, time.Duration) {
	trusted := viewerKey != "" && h.cfg.Viewers != nil && h.cfg.Viewers.ViewerTrusted(viewerKey)
	if trusted {
		source = "viewer:" + viewerKey
	}
	h.mu.Lock()
	now := time.Now()
	matched, remaining := h.passwordGate.Check(now, source, transcript, proof, h.password, h.cfg.Fixed)
	locked := !h.closed && remaining == 0 && h.passwordGate.Remaining(now) > 0
	if locked && h.passwordLockTimer == nil {
		h.passwordLockTimer = time.AfterFunc(time.Second, h.tickPasswordLock)
	}
	h.mu.Unlock()
	if locked {
		h.notify()
	}
	if trusted && matched == "" && remaining == 0 {
		if err := h.cfg.Viewers.DistrustViewer(viewerKey); err != nil {
			log.Printf("kkdesk host: forget controller: %v", err)
		}
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

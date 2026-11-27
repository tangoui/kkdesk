package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/tangoui/kkdesk/internal/pairing"
	"github.com/tangoui/kkdesk/internal/session"
	"github.com/tangoui/kkdesk/internal/unlockcred"
)

type viewerUnlock struct {
	attempt     *unlockAttempt
	message     string
	savedFailed bool
}

type unlockAttempt struct {
	account string
	save    []byte
	saved   bool
	timer   *time.Timer
}

var errCannotUnlock = errors.New("当前会话无法发送解锁请求")

func (u *viewerUnlock) clearAttempt() {
	if u.attempt == nil {
		return
	}
	if u.attempt.timer != nil {
		u.attempt.timer.Stop()
	}
	clear(u.attempt.save)
	u.attempt = nil
}

func unlockAccount(hostKey string) string {
	key, ok := pairing.PublicKey(hostKey)
	if !ok {
		return ""
	}
	fingerprint := sha256.Sum256(key)
	return hex.EncodeToString(fingerprint[:])
}

func (v *Viewer) canUnlockLocked(p *session.Peer) bool {
	return p != nil && p == v.peer && v.phase != ViewerClosed && !v.closing &&
		!v.viewOnly && !v.cfg.FilesOnly && v.unlock.attempt == nil && p.LockScreen() == session.LockScreenReady
}

// RequestSavedUnlock reports false with no error when manual input is needed.
func (v *Viewer) RequestSavedUnlock() (bool, error) {
	v.mu.Lock()
	p := v.peer
	if !v.canUnlockLocked(p) {
		v.mu.Unlock()
		return false, errCannotUnlock
	}
	account := unlockAccount(p.HostKey())
	store := v.cfg.unlockStore
	v.mu.Unlock()
	if account == "" || store == nil {
		return false, nil
	}
	v.credentialsMu.Lock()
	secret, err := store.Read(account)
	v.credentialsMu.Unlock()
	if errors.Is(err, unlockcred.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, errors.New("无法读取本机保存的解锁密码")
	}
	defer clear(secret)
	err = v.sendUnlock(p, secret, false, true)
	return err == nil, err
}

func (v *Viewer) RequestUnlock(secret []byte, remember bool) error {
	v.mu.Lock()
	p := v.peer
	v.mu.Unlock()
	return v.sendUnlock(p, secret, remember, false)
}

func (v *Viewer) sendUnlock(p *session.Peer, secret []byte, remember, saved bool) error {
	v.mu.Lock()
	if !v.canUnlockLocked(p) {
		v.mu.Unlock()
		return errCannotUnlock
	}
	attempt := &unlockAttempt{saved: saved}
	if remember && v.cfg.unlockStore != nil {
		attempt.account = unlockAccount(p.HostKey())
		if attempt.account != "" {
			attempt.save = bytes.Clone(secret)
		}
	}
	v.unlock.attempt, v.unlock.message, v.unlock.savedFailed = attempt, "", false
	if err := p.SendUnlock(secret); err != nil {
		v.unlock.clearAttempt()
		v.mu.Unlock()
		return errors.New("无法发送解锁请求")
	}
	attempt.timer = time.AfterFunc(25*time.Second, func() {
		v.mu.Lock()
		if v.unlock.attempt != attempt {
			v.mu.Unlock()
			return
		}
		clear(attempt.save)
		attempt.save = nil
		v.unlock.message = "等待解锁结果超时"
		v.mu.Unlock()
		v.notify()
	})
	v.mu.Unlock()
	v.notify()
	return nil
}

func (v *Viewer) handleUnlockResult(p *session.Peer, result session.UnlockResult) {
	v.credentialsMu.Lock()
	v.mu.Lock()
	a := v.unlock.attempt
	if a == nil || v.peer != p || v.phase == ViewerClosed {
		v.mu.Unlock()
		v.credentialsMu.Unlock()
		return
	}
	var save []byte
	switch result {
	case session.UnlockSucceeded:
		save, a.save = a.save, nil
		v.unlock.message = ""
	case session.UnlockStillLocked:
		v.unlock.savedFailed = a.saved
		v.unlock.message = "解锁密码可能不正确"
		if a.saved {
			v.unlock.message = "已保存的解锁密码不正确"
		}
	case session.UnlockUnsupported:
		v.unlock.message = "对方电脑当前无法远程解锁"
	case session.UnlockTooMany:
		v.unlock.message = "本次连接的解锁尝试次数已达上限"
	}
	v.unlock.clearAttempt()
	v.mu.Unlock()
	if len(save) != 0 {
		err := v.cfg.unlockStore.Write(a.account, save)
		clear(save)
		if err != nil {
			v.mu.Lock()
			v.unlock.message = "已解锁，无法保存密码"
			v.mu.Unlock()
		}
	}
	v.credentialsMu.Unlock()
	v.notify()
}

func (v *Viewer) ForgetUnlock() error {
	v.credentialsMu.Lock()
	v.mu.Lock()
	p := v.peer
	store := v.cfg.unlockStore
	if p == nil || store == nil || v.unlock.attempt != nil {
		v.mu.Unlock()
		v.credentialsMu.Unlock()
		return errCannotUnlock
	}
	account := unlockAccount(p.HostKey())
	v.mu.Unlock()
	if account == "" {
		v.credentialsMu.Unlock()
		return errCannotUnlock
	}
	if err := store.Delete(account); err != nil && !errors.Is(err, unlockcred.ErrNotFound) {
		v.credentialsMu.Unlock()
		return errors.New("无法删除本机保存的解锁密码")
	}
	v.mu.Lock()
	v.unlock.savedFailed, v.unlock.message = false, ""
	v.mu.Unlock()
	v.credentialsMu.Unlock()
	v.notify()
	return nil
}

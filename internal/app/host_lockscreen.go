package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/tangoui/kkdesk/internal/capture"
	"github.com/tangoui/kkdesk/internal/input"
	"github.com/tangoui/kkdesk/internal/lockscreen"
	"github.com/tangoui/kkdesk/internal/session"
)

type hostDesktop struct {
	h           *Host
	video       *videoSender
	opts        capture.Options
	releaseKeys func()

	mu          sync.Mutex
	locked      bool
	normal      capture.Session
	helper      lockscreen.Session
	held        input.Held
	next        time.Time // earliest retry after a failed helper connection
	unavailable session.LockScreenState
}

func (d *hostDesktop) refresh(ctx context.Context) error {
	if ctx.Err() != nil {
		return nil
	}
	locked, err := d.h.cfg.screenLocked()
	d.mu.Lock()
	previous, normal, helper := d.locked, d.normal, d.helper
	d.mu.Unlock()
	if err != nil {
		if helper != nil {
			d.stopSource()
		}
		if previous {
			return nil
		}
	}
	if locked != previous {
		d.mu.Lock()
		d.locked = locked
		d.unavailable = session.LockScreenUnknown
		d.mu.Unlock()
		d.releaseKeys()
		d.stopSource()
		normal, helper = nil, nil
		d.next = time.Time{}
	}
	if !locked {
		if normal != nil {
			return nil
		}
		if help := d.h.cfg.captureHelp(); help != "" {
			d.h.setErr(help)
			return errCapturePermission
		}
		normal, err = d.h.cfg.startDesktop(d.options(), d.video.frame)
		if err != nil {
			if locked, queryErr := d.h.cfg.screenLocked(); queryErr == nil && locked {
				d.mu.Lock()
				d.locked = true
				d.mu.Unlock()
				d.releaseKeys()
				return d.startLocked(ctx)
			}
			return fmt.Errorf("启动屏幕采集失败：%w", err)
		}
		d.mu.Lock()
		d.normal = normal
		d.mu.Unlock()
		d.attach(normal)
		return nil
	}
	if helper != nil {
		select {
		case <-helper.Done():
			d.stopSource()
		default:
			return nil
		}
	}
	return d.startLocked(ctx)
}

func (d *hostDesktop) startLocked(ctx context.Context) error {
	d.mu.Lock()
	d.locked = true
	d.mu.Unlock()
	if d.h.cfg.startLockScreen == nil || time.Now().Before(d.next) {
		return nil
	}
	d.next = time.Now().Add(time.Second)
	helper, err := d.h.cfg.startLockScreen(ctx, d.options(), d.video.frame)
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		state := lockScreenStartFailure(err)
		d.mu.Lock()
		changed := d.unavailable != state
		d.unavailable = state
		d.mu.Unlock()
		if changed {
			// Start errors contain component, OS and IPC details only.
			// Unlock requests use a separate path.
			log.Printf("kkdesk host: start lock screen capture: %v", err)
		}
		d.h.setLockScreenFailure(state)
		return nil
	}
	d.mu.Lock()
	d.helper = helper
	d.unavailable = session.LockScreenUnknown
	d.mu.Unlock()
	d.h.ClearLockScreenFailure()
	d.attach(helper)
	return nil
}

func lockScreenStartFailure(err error) session.LockScreenState {
	switch {
	case errors.Is(err, lockscreen.ErrNotInstalled):
		return session.LockScreenComponentUnavailable
	case errors.Is(err, lockscreen.ErrWrongLocation):
		return session.LockScreenWrongInstallLocation
	case errors.Is(err, lockscreen.ErrService):
		return session.LockScreenServiceUnavailable
	case errors.Is(err, lockscreen.ErrNeedsUpdate):
		return session.LockScreenComponentNeedsUpdate
	case errors.Is(err, lockscreen.ErrNeedsSignature):
		return session.LockScreenSignatureRequired
	case errors.Is(err, lockscreen.ErrNeedsApproval):
		return session.LockScreenApprovalRequired
	case errors.Is(err, lockscreen.ErrUnsupported):
		return session.LockScreenPlatformUnsupported
	default:
		return session.LockScreenComponentError
	}
}

func (d *hostDesktop) options() capture.Options {
	opts := d.opts
	d.video.mu.Lock()
	opts.FPS = d.video.fps
	d.video.mu.Unlock()
	return opts
}

func (d *hostDesktop) attach(source capture.Session) {
	d.video.mu.Lock()
	d.video.capt = source
	fps := d.video.fps
	d.video.mu.Unlock()
	if fps != d.opts.FPS {
		source.SetFPS(fps)
	}
}

func (d *hostDesktop) stopSource() {
	d.mu.Lock()
	normal, helper, ups := d.normal, d.helper, d.held.Release()
	d.normal, d.helper = nil, nil
	d.mu.Unlock()
	d.video.mu.Lock()
	d.video.capt = nil
	d.video.mu.Unlock()
	if helper != nil {
		for _, e := range ups {
			_ = helper.Apply(e)
		}
		helper.Close()
	}
	if normal != nil {
		st := normal.Stats()
		normal.Close()
		log.Printf("kkdesk host: capture frames complete=%d idle=%d other=%d display-after-callback=%d resent=%d reopened=%d",
			st.Complete, st.Idle, st.Other, st.Late, st.Resent, st.Reopened)
	}
	d.video.mu.Lock()
	defer d.video.mu.Unlock()
	if d.video.enc != nil {
		d.video.enc.Close()
		d.video.enc = nil
	}
	d.video.forceKey.Store(true)
}

func (d *hostDesktop) close() { d.stopSource() }

func (d *hostDesktop) state() session.LockScreenState {
	d.mu.Lock()
	defer d.mu.Unlock()
	switch {
	case !d.locked:
		return session.LockScreenUnlocked
	case d.helper != nil:
		return session.LockScreenReady
	case d.unavailable != session.LockScreenUnknown:
		return d.unavailable
	case d.h.cfg.startLockScreen != nil:
		return session.LockScreenUnknown
	default:
		return session.LockScreenPlatformUnsupported
	}
}

// input consumes all remote input while locked, including when no helper
// is available. The caller uses the local injector only for the desktop.
func (d *hostDesktop) input(e input.Event) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.locked && d.helper != nil && d.helper.Apply(e) == nil {
		d.held.Track(e)
	}
	return d.locked
}

func (d *hostDesktop) stats() capture.Stats {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.normal != nil {
		return d.normal.Stats()
	}
	if d.helper != nil {
		return d.helper.Stats()
	}
	return capture.Stats{}
}

func (d *hostDesktop) excludeWindows(ids []uint32) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.normal == nil {
		return nil
	}
	return capture.ExcludeWindows(d.normal, ids)
}

func (h *Host) serveUnlock(ctx context.Context, p *session.Peer, desktop *hostDesktop) <-chan struct{} {
	done := make(chan struct{})
	queue := make(chan []byte, 1)
	var mu sync.Mutex
	p.OnUnlock(func(secret []byte) {
		mu.Lock()
		defer mu.Unlock()
		if ctx.Err() != nil || len(queue) != 0 {
			return
		}
		queue <- bytes.Clone(secret)
	})
	go func() {
		defer close(done)
		defer func() {
			p.OnUnlock(nil)
			mu.Lock()
			defer mu.Unlock()
			select {
			case secret := <-queue:
				clear(secret)
			default:
			}
		}()
		attempts := 0
		for {
			select {
			case <-ctx.Done():
				return
			case secret := <-queue:
				desktop.mu.Lock()
				helper := desktop.helper
				desktop.mu.Unlock()
				result := h.tryUnlock(ctx, helper, secret, &attempts)
				clear(secret)
				if ctx.Err() == nil {
					_ = p.SendUnlockResult(result)
				}
			}
		}
	}()
	return done
}

func (h *Host) tryUnlock(parent context.Context, helper lockscreen.Session, secret []byte, attempts *int) session.UnlockResult {
	if parent.Err() != nil {
		return session.UnlockUnsupported
	}
	locked, err := h.cfg.screenLocked()
	if err != nil {
		return session.UnlockUnsupported
	}
	if !locked {
		return session.UnlockSucceeded
	}
	if helper == nil {
		return session.UnlockUnsupported
	}
	if *attempts >= 3 {
		h.mu.Lock()
		h.notice = "本次远程连接的解锁尝试已达上限"
		h.mu.Unlock()
		h.notify()
		return session.UnlockTooMany
	}
	*attempts = *attempts + 1
	ctx, cancel := context.WithTimeout(parent, h.cfg.unlockTimeout)
	defer cancel()
	err = helper.Unlock(ctx, secret)
	clear(secret)
	if locked, queryErr := h.cfg.screenLocked(); queryErr == nil && !locked {
		return session.UnlockSucceeded
	}
	if errors.Is(err, lockscreen.ErrStillLocked) || errors.Is(err, context.DeadlineExceeded) {
		return session.UnlockStillLocked
	}
	if err != nil {
		return session.UnlockUnsupported
	}
	ticker := time.NewTicker(150 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return session.UnlockStillLocked
		case <-ticker.C:
			if locked, err := h.cfg.screenLocked(); err == nil && !locked {
				return session.UnlockSucceeded
			}
		}
	}
}

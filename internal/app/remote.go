package app

import (
	"context"
	"time"

	"github.com/coder/websocket"

	"github.com/tangoui/kkdesk/internal/rendezvous"
	"github.com/tangoui/kkdesk/internal/session"
)

type remoteHostState struct {
	code   string
	cancel context.CancelFunc
	online bool
	err    string
}

func (h *Host) SetRemote(cfg rendezvous.Config) {
	h.mu.Lock()
	defer h.notify()
	defer h.mu.Unlock()
	if h.closed || h.cfg.Remote == cfg {
		return
	}
	h.stopRemoteLocked()
	h.cfg.Remote = cfg
	h.remote.code = h.deviceCode()
	if h.wantOn {
		h.startRemoteLocked()
	}
}

func (h *Host) startRemoteLocked() {
	if h.remote.cancel != nil || h.cfg.Remote.URL == "" || h.cfg.Pairing == nil {
		return
	}
	if err := h.cfg.Remote.Validate(); err != nil {
		h.remote.err = err.Error()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	h.remote.cancel = cancel
	cfg, code := h.cfg.Remote, h.remote.code
	h.wg.Add(1)
	go func() {
		defer h.wg.Done()
		h.serveRemote(ctx, cfg, code)
	}()
	if h.cfg.Pairing != nil {
		h.wg.Go(func() { h.runMobileRemote(ctx, cfg) })
	}
}

func (h *Host) stopRemoteLocked() {
	if h.remote.cancel != nil {
		h.remote.cancel()
		h.remote.cancel = nil
	}
	h.remote.online, h.remote.err = false, ""
	if !h.busy && h.ln == nil {
		h.phase = HostOff
	}
}

func (h *Host) remoteStatus(ctx context.Context, online bool, reason string) {
	h.mu.Lock()
	if ctx.Err() != nil {
		h.mu.Unlock()
		return
	}
	h.remote.online, h.remote.err = online, reason
	if !h.busy && h.ln == nil {
		h.phase = HostOff
		if online {
			h.phase = HostListening
		}
	}
	h.mu.Unlock()
	h.notify()
}

func (h *Host) serveRemote(ctx context.Context, cfg rendezvous.Config, code string) {
	for ctx.Err() == nil {
		dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		conn, err := rendezvous.Register(dialCtx, cfg, code)
		cancel()
		if err == nil {
			h.remoteStatus(ctx, true, "")
			err = h.remoteOffers(ctx, cfg, conn)
			_ = conn.CloseNow()
		}
		if ctx.Err() != nil {
			return
		}
		h.remoteStatus(ctx, false, err.Error())
		wait := time.NewTimer(3 * time.Second)
		select {
		case <-ctx.Done():
			wait.Stop()
			return
		case <-wait.C:
		}
	}
}

func (h *Host) remoteOffers(ctx context.Context, cfg rendezvous.Config, conn *websocket.Conn) error {
	// Keep reading control frames while ICE gathering runs for a request.
	for {
		req, err := rendezvous.ReadOffer(ctx, conn)
		if err != nil {
			return err
		}
		h.wg.Add(1)
		go func() {
			defer h.wg.Done()
			h.answerRemote(ctx, cfg, conn, req)
		}()
	}
}

func (h *Host) answerRemote(ctx context.Context, cfg rendezvous.Config, conn *websocket.Conn, req rendezvous.Offer) {
	replyCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	peer, answer, transcript, err := session.AnswerRemote(cfg, req, h.cfg.Pairing)
	if err != nil {
		_ = rendezvous.Reply(replyCtx, conn, req.ID, nil, "被控端无法建立跨网连接")
		return
	}
	if err := rendezvous.Reply(replyCtx, conn, req.ID, answer, ""); err != nil {
		peer.Close()
		return
	}
	if err := peer.AcceptPassword(replyCtx, transcript, h.verifyPassword); err != nil {
		peer.Close()
		return
	}
	h.handle(peer, ctx, true, "", "")
}

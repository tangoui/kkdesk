package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tangoui/kkdesk/internal/pairing"
	"github.com/tangoui/kkdesk/internal/rendezvous"
	"github.com/tangoui/kkdesk/internal/session"
)

func testHostIdentity(t *testing.T) *pairing.Store {
	t.Helper()
	identity, err := pairing.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return identity
}

func TestHostWithoutIdentityCannotListen(t *testing.T) {
	h := NewHost(HostConfig{Addr: "127.0.0.1:0"}, nil)
	defer h.Close()
	if err := h.SetEnabled(true); err == nil || h.ListenAddr() != "" || h.State().Err == "" {
		t.Fatalf("missing identity did not block listening: %v, %+v", err, h.State())
	}
}

func TestPasswordLockoutSharedByLANAndRemote(t *testing.T) {
	cfg, stop := remoteTestService(t)
	defer stop()
	h := NewHost(HostConfig{Addr: "127.0.0.1:0", Password: "good", Pairing: testHostIdentity(t), Remote: cfg, AutoAccept: true}, nil)
	defer h.Close()
	if err := h.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	for h.State().RemoteCode == "" && ctx.Err() == nil {
		time.Sleep(10 * time.Millisecond)
	}
	for range 4 {
		if _, err := session.Dial(ctx, h.ListenAddr(), "bad"); !errors.Is(err, session.ErrBadPassword) {
			t.Fatalf("bad LAN password: %v", err)
		}
	}
	if _, err := session.DialRemote(ctx, cfg, h.State().RemoteCode, session.ModeFiles, "bad"); !errors.Is(err, session.ErrBadPassword) {
		t.Fatalf("bad remote password: %v", err)
	}
	if state := h.State(); state.PasswordLockSeconds == 0 || state.Phase != HostListening {
		t.Fatalf("host after five incorrect passwords: %+v", state)
	}
	for _, dial := range []func() (*session.Peer, error){
		func() (*session.Peer, error) { return session.Dial(ctx, h.ListenAddr(), "good") },
		func() (*session.Peer, error) {
			return session.DialRemote(ctx, cfg, h.State().RemoteCode, session.ModeFiles, "good")
		},
	} {
		if _, err := dial(); !errors.As(err, new(*session.PasswordLockedError)) {
			t.Fatalf("locked password: %v", err)
		}
	}
}

func TestRemoteRejectsWrongHostIdentity(t *testing.T) {
	cfg, stop := remoteTestService(t)
	defer stop()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	want := testHostIdentity(t).DeviceCode()
	other := testHostIdentity(t)
	host, err := rendezvous.Register(ctx, cfg, want)
	if err != nil {
		t.Fatal(err)
	}
	defer host.CloseNow()
	replied := make(chan error, 1)
	go func() {
		req, err := rendezvous.ReadOffer(ctx, host)
		if err != nil {
			replied <- err
			return
		}
		peer, answer, _, err := session.AnswerRemote(cfg, req, other)
		if err == nil {
			defer peer.Close()
			err = rendezvous.Reply(ctx, host, req.ID, answer, "")
		}
		replied <- err
	}()
	peer, err := session.DialRemote(ctx, cfg, want, session.ModeFiles, "good")
	if peer != nil {
		peer.Close()
	}
	if !errors.Is(err, session.ErrWrongHostCode) {
		t.Fatalf("connection with wrong host identity: %v", err)
	}
	if err := <-replied; err != nil {
		t.Fatal(err)
	}
}

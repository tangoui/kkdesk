package app

import (
	"bytes"
	"context"
	"sync"
	"testing"
	"time"

	"github.com/tangoui/kkdesk/internal/capture"
	"github.com/tangoui/kkdesk/internal/lockscreen"
	"github.com/tangoui/kkdesk/internal/session"
	"github.com/tangoui/kkdesk/internal/unlockcred"
)

type testUnlockStore struct {
	mu     sync.Mutex
	values map[string][]byte
}

func (s *testUnlockStore) Read(account string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.values[account]
	if !ok {
		return nil, unlockcred.ErrNotFound
	}
	return bytes.Clone(p), nil
}

func (s *testUnlockStore) Write(account string, secret []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[account] = bytes.Clone(secret)
	return nil
}

func (s *testUnlockStore) Delete(account string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.values, account)
	return nil
}

func TestViewerUnlockSavedFailureAndForget(t *testing.T) {
	identity := testHostIdentity(t)
	helper := &testLockHelper{
		ended: make(chan struct{}),
		onUnlock: func(context.Context) error {
			return lockscreen.ErrStillLocked
		},
	}
	h := NewHost(HostConfig{
		Addr: "127.0.0.1:0", Password: "123456", Pairing: identity, AutoAccept: true,
		screenLocked: func() (bool, error) { return true, nil },
		startLockScreen: func(context.Context, capture.Options, func(capture.Frame)) (lockscreen.Session, error) {
			return helper, nil
		},
	}, nil)
	t.Cleanup(h.Close)
	if err := h.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	store := &testUnlockStore{values: make(map[string][]byte)}
	account := unlockAccount(identity.PublicKey())
	if account == "" || account == unlockAccount(testHostIdentity(t).PublicKey()) {
		t.Fatal("host identities did not produce separate accounts")
	}
	_ = store.Write(account, []byte("saved PIN"))
	v := NewViewer(ViewerConfig{Addr: h.ListenAddr(), Password: "123456", unlockStore: store}, nil)
	t.Cleanup(v.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := v.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "locked host", func() bool { return v.State().LockScreen == session.LockScreenReady })
	if sent, err := v.RequestSavedUnlock(); sent || err == nil {
		t.Fatal("view-only could submit an unlock")
	}
	v.SetViewOnly(false)
	if sent, err := v.RequestSavedUnlock(); !sent || err != nil {
		t.Fatalf("saved unlock was not sent: %v", err)
	}
	waitFor(t, "saved password error", func() bool { return v.State().UnlockSavedFailed })
	if err := v.ForgetUnlock(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Read(account); err != unlockcred.ErrNotFound {
		t.Fatal("credential was not removed")
	}
	if sent, err := v.RequestSavedUnlock(); sent || err != nil {
		t.Fatalf("missing password should prompt for input: sent=%t err=%v", sent, err)
	}
}

func TestViewerUnlockPersistsOnlyAfterSuccess(t *testing.T) {
	store := &testUnlockStore{values: make(map[string][]byte)}
	v := NewViewer(ViewerConfig{Addr: "testhost", unlockStore: store}, nil)
	secret := []byte("replacement PIN")
	v.unlock = viewerUnlock{attempt: &unlockAttempt{account: "host-a", save: bytes.Clone(secret)}}
	v.handleUnlockResult(nil, session.UnlockStillLocked)
	if _, err := store.Read("host-a"); err != unlockcred.ErrNotFound {
		t.Fatal("failed credential was saved")
	}
	v.unlock = viewerUnlock{attempt: &unlockAttempt{account: "host-a", save: bytes.Clone(secret)}}
	v.handleUnlockResult(nil, session.UnlockSucceeded)
	saved, err := store.Read("host-a")
	if err != nil || !bytes.Equal(saved, secret) {
		t.Fatal("successful credential was not saved")
	}
}

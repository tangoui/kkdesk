package app

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tangoui/kkdesk/internal/config"
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

func TestPasswordLockoutPerPathAndLiftedByRefresh(t *testing.T) {
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
	for range 5 {
		if _, err := session.DialRemote(ctx, cfg, h.State().RemoteCode, session.ModeFiles, "bad"); !errors.Is(err, session.ErrBadPassword) {
			t.Fatalf("bad remote password: %v", err)
		}
	}
	if state := h.State(); state.PasswordLockSeconds == 0 || state.Phase != HostListening {
		t.Fatalf("host after five incorrect remote passwords: %+v", state)
	}
	if _, err := session.DialRemote(ctx, cfg, h.State().RemoteCode, session.ModeFiles, "good"); !errors.As(err, new(*session.PasswordLockedError)) {
		t.Fatalf("remote while paused: %v", err)
	}
	// Strangers failing across the network do not lock the LAN out.
	lan, err := session.Dial(ctx, h.ListenAddr(), "good")
	if err != nil {
		t.Fatalf("LAN while remote is paused: %v", err)
	}
	lan.Close()
	waitIdle(t, h)
	// A new password lifts every pause.
	h.RefreshPassword()
	good := h.State().Password
	peer, err := session.DialRemote(ctx, cfg, h.State().RemoteCode, session.ModeFiles, good)
	if err != nil {
		t.Fatalf("remote after refreshing the password: %v", err)
	}
	peer.Close()
}

// waitIdle waits until the host is listening again after a session.
func waitIdle(t *testing.T, h *Host) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for h.State().Phase != HostListening {
		if time.Now().After(deadline) {
			t.Fatalf("host did not return to listening: %+v", h.State())
		}
		time.Sleep(10 * time.Millisecond)
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

func TestViewerRemembersLANIdentity(t *testing.T) {
	store, err := config.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	host := NewHost(HostConfig{Addr: "127.0.0.1:0", Password: "good", Pairing: testHostIdentity(t), AutoAccept: true}, nil)
	defer host.Close()
	if err := host.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	address := host.ListenAddr()
	legacy, err := store.AddDevice("电脑", address, "")
	if err != nil {
		t.Fatal(err)
	}
	dial := func(password string) (*Viewer, error) {
		viewer := NewViewer(ViewerConfig{Addr: address, Password: password, FilesOnly: true, Devices: store}, nil)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return viewer, viewer.Connect(ctx)
	}
	if v, err := dial("bad"); err == nil {
		v.Close()
		t.Fatal("wrong password was accepted")
	}
	if d, _ := store.Device(legacy.ID); d.HostKey != "" {
		t.Fatal("failed password saved host identity")
	}
	first, err := dial("good")
	defer first.Close()
	if err != nil || first.State().FirstHostCode != host.State().DeviceCode {
		t.Fatalf("first connection: %+v, %v", first.State(), err)
	}
	if d, _ := store.Device(legacy.ID); d.HostKey != host.cfg.Pairing.PublicKey() {
		t.Fatalf("saved key = %q", d.HostKey)
	}
	closeAndWait(t, host, first)
	second, err := dial("good")
	defer second.Close()
	if err != nil || second.State().FirstHostCode != "" {
		t.Fatalf("reconnect: %+v, %v", second.State(), err)
	}
	closeAndWait(t, host, second)
	host.Close()

	replaced := NewHost(HostConfig{Addr: address, Password: "good", Pairing: testHostIdentity(t), AutoAccept: true}, nil)
	defer replaced.Close()
	if err := replaced.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	if viewer, err := dial("good"); err == nil || !strings.Contains(err.Error(), "身份变了") {
		viewer.Close()
		t.Fatalf("replacement identity was accepted: %v", err)
	}
	if err := store.DeleteDevice(legacy.ID); err != nil {
		t.Fatal(err)
	}
	newViewer, err := dial("good")
	defer newViewer.Close()
	if err != nil || newViewer.State().FirstHostCode != replaced.State().DeviceCode {
		t.Fatalf("reconnect after removing identity: %+v, %v", newViewer.State(), err)
	}
}

// trustSet is an in-memory ViewerTrust.
type trustSet struct {
	mu   sync.Mutex
	keys map[string]bool
}

func (s *trustSet) ViewerTrusted(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.keys[key]
}

func (s *trustSet) TrustViewer(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.keys == nil {
		s.keys = map[string]bool{}
	}
	s.keys[key] = true
	return nil
}

// A controller that has got in with the password before is not held up by a
// pause strangers caused on the shared cross-network source.
func TestTrustedViewerIgnoresRemotePause(t *testing.T) {
	cfg, stop := remoteTestService(t)
	defer stop()
	trust := &trustSet{}
	h := NewHost(HostConfig{Addr: "127.0.0.1:0", Password: "good", Pairing: testHostIdentity(t), Remote: cfg,
		AutoAccept: true, Rotate: config.RotateNever, Viewers: trust}, nil)
	defer h.Close()
	if err := h.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	for h.State().RemoteCode == "" && ctx.Err() == nil {
		time.Sleep(10 * time.Millisecond)
	}
	code := h.State().RemoteCode
	me := testHostIdentity(t) // the controller's own identity
	p, err := session.DialRemoteAs(ctx, cfg, code, session.ModeFiles, "good", me)
	if err != nil {
		t.Fatalf("first connection: %v", err)
	}
	p.Close()
	waitIdle(t, h)
	if !trust.ViewerTrusted(me.PublicKey()) {
		t.Fatal("a controller that got in was not remembered")
	}
	for range 5 { // strangers pause the shared remote source
		if _, err := session.DialRemote(ctx, cfg, code, session.ModeFiles, "bad"); !errors.Is(err, session.ErrBadPassword) {
			t.Fatalf("stranger: %v", err)
		}
	}
	if _, err := session.DialRemote(ctx, cfg, code, session.ModeFiles, "good"); !errors.As(err, new(*session.PasswordLockedError)) {
		t.Fatalf("anonymous controller during the pause: %v", err)
	}
	stranger := testHostIdentity(t)
	if _, err := session.DialRemoteAs(ctx, cfg, code, session.ModeFiles, "good", stranger); !errors.As(err, new(*session.PasswordLockedError)) {
		t.Fatalf("an unknown identity during the pause: %v", err)
	}
	p, err = session.DialRemoteAs(ctx, cfg, code, session.ModeFiles, "good", me)
	if err != nil {
		t.Fatalf("trusted controller during the pause: %v", err)
	}
	p.Close()
}

// On a first connection to an address the user checks the device code before
// anything derived from the password is sent; declining sends nothing.
func TestViewerConfirmsFirstHostBeforeProof(t *testing.T) {
	store, err := config.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	host := NewHost(HostConfig{Addr: "127.0.0.1:0", Password: "good", Pairing: testHostIdentity(t), AutoAccept: true}, nil)
	defer host.Close()
	if err := host.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	address := host.ListenAddr()
	var asked []string
	answer := false
	dial := func() (*Viewer, error) {
		v := NewViewer(ViewerConfig{Addr: address, Password: "good", FilesOnly: true, Devices: store,
			ConfirmHost: func(_ context.Context, addr, code string) bool {
				mu.Lock()
				defer mu.Unlock()
				asked = append(asked, code)
				return answer
			}}, nil)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return v, v.Connect(ctx)
	}
	if v, err := dial(); err == nil {
		v.Close()
		t.Fatal("connected although the user declined the device code")
	}
	if len(asked) != 1 || asked[0] != host.State().DeviceCode {
		t.Fatalf("asked %q, want the host's device code %q once", asked, host.State().DeviceCode)
	}
	if key := store.HostKey(address); key != "" {
		t.Fatalf("a declined first connection still connected and remembered %q", key)
	}
	answer = true
	v, err := dial()
	if err != nil {
		t.Fatalf("after confirming: %v", err)
	}
	closeAndWait(t, host, v)
	asked = nil
	v, err = dial()
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	if len(asked) != 0 {
		t.Fatalf("asked again for a known host: %q", asked)
	}
}

// A stranger at the address cannot skip the device-code check by naming no
// key or refusing the probe: the user is asked (with no code to compare) or
// the connection fails, and no proof is sent unasked.
func TestViewerFirstHostCheckCannotBeSkipped(t *testing.T) {
	store, err := config.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() { // an impostor: greets without a key, then reads whatever comes
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				_, _ = c.Write([]byte("{\"version\":2}\n"))
				_, _ = io.Copy(io.Discard, c)
			}()
		}
	}()
	var asked []string
	v := NewViewer(ViewerConfig{Addr: ln.Addr().String(), Password: "good", FilesOnly: true, Devices: store,
		ConfirmHost: func(_ context.Context, addr, code string) bool {
			asked = append(asked, code)
			return false
		}}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := v.Connect(ctx); err == nil || !strings.Contains(err.Error(), ErrHostNotConfirmed.Error()) {
		t.Fatalf("Connect = %v, want ErrHostNotConfirmed", err)
	}
	if len(asked) != 1 || asked[0] != "" {
		t.Fatalf("asked %q, want one question with no code to compare", asked)
	}
	ln.Close() // a host that refuses the probe
	v = NewViewer(ViewerConfig{Addr: ln.Addr().String(), Password: "good", FilesOnly: true, Devices: store,
		ConfirmHost: func(context.Context, string, string) bool { t.Error("asked without a probe"); return true }}, nil)
	if err := v.Connect(ctx); err == nil {
		t.Fatal("connected although the probe failed")
	}
}

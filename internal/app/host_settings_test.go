package app

import (
	"context"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tangoui/kkdesk/internal/config"
)

// dialFiles connects a files-only viewer and returns it, or the error.
func dialFiles(t *testing.T, addr, pw string) (*Viewer, error) {
	t.Helper()
	v := NewViewer(ViewerConfig{Addr: addr, Password: pw, FilesOnly: true}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := v.Connect(ctx); err != nil {
		return nil, err
	}
	return v, nil
}

// closeAndWait ends a viewer's session and waits until the host is free.
func closeAndWait(t *testing.T, h *Host, v *Viewer) {
	t.Helper()
	v.Close()
	waitFor(t, "host free", func() bool { return h.State().Phase != HostActive })
}

func TestHostFixedPassword(t *testing.T) {
	h := NewHost(HostConfig{Pairing: testHostIdentity(t), Addr: "127.0.0.1:0", Password: "123456", AutoAccept: true, Rotate: config.RotateNever}, nil)
	defer h.Close()
	h.SetFixedPassword("fixedpw1")
	if err := h.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	if !h.State().HasFixed {
		t.Fatal("HasFixed false")
	}
	for _, pw := range []string{"123456", "fixedpw1"} {
		v, err := dialFiles(t, h.ListenAddr(), pw)
		if err != nil {
			t.Fatalf("password %q: %v", pw, err)
		}
		closeAndWait(t, h, v)
	}
	h.SetFixedPassword("fixedpw2")
	if _, err := dialFiles(t, h.ListenAddr(), "fixedpw1"); err == nil || !strings.Contains(err.Error(), "密码错误") {
		t.Fatalf("old fixed password: %v, want 密码错误", err)
	}
	v, err := dialFiles(t, h.ListenAddr(), "fixedpw2")
	if err != nil {
		t.Fatal(err)
	}
	closeAndWait(t, h, v)
	h.SetFixedPassword("")
	if _, err := dialFiles(t, h.ListenAddr(), "fixedpw2"); err == nil {
		t.Fatal("cleared fixed password still works")
	}
}

func TestHostSessionPermissionsResetToDefaults(t *testing.T) {
	h := NewHost(HostConfig{Pairing: testHostIdentity(t), Addr: "127.0.0.1:0", Password: "123456",
		DisableSessionClipboard: true, DisableSessionFiles: true}, nil)
	defer h.Close()
	if err := h.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	v, err := dialFiles(t, h.ListenAddr(), "123456")
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	waitFor(t, "first confirmation", func() bool { return h.State().Phase == HostPending })
	if s := h.State(); s.ClipboardAccess || s.FileAccess {
		t.Fatalf("first session permissions: %+v", s)
	}
	h.SetClipboardAccess(true)
	h.SetFileAccess(true)
	h.Accept()
	waitFor(t, "active session", func() bool { return h.State().Phase == HostActive })
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := v.Files().Home(ctx); err != nil {
		t.Fatal(err)
	}
	h.SetSessionDefaults(true, false)
	if s := h.State(); !s.ClipboardAccess || !s.FileAccess {
		t.Fatalf("changing defaults affected the current session: %+v", s)
	}
	closeAndWait(t, h, v)
	next, err := dialFiles(t, h.ListenAddr(), "123456")
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	waitFor(t, "next confirmation", func() bool { return h.State().Phase == HostPending })
	if s := h.State(); !s.ClipboardAccess || s.FileAccess {
		t.Fatalf("next session permissions: %+v", s)
	}
}

func TestHostRotatesAfterSession(t *testing.T) {
	var mu sync.Mutex
	var saved []string
	h := NewHost(HostConfig{Pairing: testHostIdentity(t), Addr: "127.0.0.1:0", Password: "123456", AutoAccept: true, Rotate: config.RotateAfterSession,
		OnPassword: func(p config.TempPassword) { mu.Lock(); saved = append(saved, p.Password); mu.Unlock() }}, nil)
	defer h.Close()
	if err := h.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	// A wrong password is no session: no rotation.
	if _, err := dialFiles(t, h.ListenAddr(), "000000"); err == nil {
		t.Fatal("wrong password accepted")
	}
	if h.State().Password != "123456" {
		t.Fatal("rotated after a failed attempt")
	}
	v, err := dialFiles(t, h.ListenAddr(), "123456")
	if err != nil {
		t.Fatal(err)
	}
	if h.State().Password != "123456" {
		t.Fatal("rotated during the session")
	}
	closeAndWait(t, h, v)
	waitFor(t, "rotation", func() bool { return h.State().Password != "123456" })
	mu.Lock()
	n := len(saved)
	mu.Unlock()
	if n != 1 {
		t.Fatalf("OnPassword called %d times", n)
	}
	if _, err := dialFiles(t, h.ListenAddr(), "123456"); err == nil {
		t.Fatal("old temporary password still works")
	}

	// 不自动更换: the password stays.
	h.SetRotate(config.RotateNever)
	pw := h.State().Password
	v, err = dialFiles(t, h.ListenAddr(), pw)
	if err != nil {
		t.Fatal(err)
	}
	closeAndWait(t, h, v)
	time.Sleep(100 * time.Millisecond)
	if h.State().Password != pw {
		t.Fatal("rotated with RotateNever")
	}
}

func TestHostTimedRotation(t *testing.T) {
	old := time.Now().Add(-13 * time.Hour)
	h := NewHost(HostConfig{Pairing: testHostIdentity(t), Addr: "127.0.0.1:0", Password: "123456", PasswordSince: old, Rotate: config.Rotate24h}, nil)
	defer h.Close()
	if h.State().Password != "123456" {
		t.Fatal("rotated before 24 h")
	}
	h.SetRotate(config.Rotate12h) // 13 h old: due
	if s := h.State(); s.Password == "123456" || time.Since(s.PasswordSince) > time.Minute {
		t.Fatalf("not rotated when due: %+v", s)
	}
	h2 := NewHost(HostConfig{Pairing: testHostIdentity(t), Addr: "127.0.0.1:0", Password: "123456", PasswordSince: time.Now().Add(-25 * time.Hour), Rotate: config.Rotate24h}, nil)
	defer h2.Close()
	if h2.State().Password == "123456" {
		t.Fatal("an expired saved password was kept at start")
	}
}

// Changing the port restarts the listener only: the running session goes
// on, the old port stops accepting, the new one accepts.
func TestHostSetAddrKeepsSession(t *testing.T) {
	h := NewHost(HostConfig{Pairing: testHostIdentity(t), Addr: "127.0.0.1:0", Password: "123456", AutoAccept: true, Rotate: config.RotateNever}, nil)
	defer h.Close()
	if err := h.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	oldAddr := h.ListenAddr()
	v, err := dialFiles(t, oldAddr, "123456")
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := v.Files().Home(ctx); err != nil {
		t.Fatal(err)
	}
	free, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	freeAddr := free.Addr().String()
	free.Close()
	if err := h.SetAddr(freeAddr); err != nil {
		t.Fatal(err)
	}
	newAddr := h.ListenAddr()
	if newAddr == "" || newAddr == oldAddr {
		t.Fatalf("listen address %q after SetAddr (was %q)", newAddr, oldAddr)
	}
	if st := h.State(); st.Phase != HostActive {
		t.Fatalf("session ended by SetAddr: %+v", st)
	}
	if _, err := v.Files().Home(ctx); err != nil {
		t.Fatalf("session broken after SetAddr: %v", err)
	}
	if _, err := dialFiles(t, oldAddr, "123456"); err == nil {
		t.Fatal("old port still accepts")
	}
	closeAndWait(t, h, v)
	v2, err := dialFiles(t, newAddr, "123456")
	if err != nil {
		t.Fatalf("new port: %v", err)
	}
	closeAndWait(t, h, v2)
}

func TestHostConfirmSetting(t *testing.T) {
	h := NewHost(HostConfig{Pairing: testHostIdentity(t), Addr: "127.0.0.1:0", Password: "123456", Rotate: config.RotateNever}, nil)
	defer h.Close()
	if err := h.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	h.SetAutoAccept(true)
	v, err := dialFiles(t, h.ListenAddr(), "123456")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "active without confirmation", func() bool { return h.State().Phase == HostActive })
	closeAndWait(t, h, v)
	h.SetAutoAccept(false)
	vc := make(chan *Viewer, 1)
	addr := h.ListenAddr()
	go func() {
		v := NewViewer(ViewerConfig{Addr: addr, Password: "123456", FilesOnly: true}, nil)
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_ = v.Connect(ctx)
		vc <- v
	}()
	waitFor(t, "pending", func() bool { return h.State().Phase == HostPending })
	h.Reject()
	(<-vc).Close()
}

// Turning remote control off cancels a LAN request still waiting for the
// user: a late 接受 must not start the session.
func TestHostDisableCancelsPendingLAN(t *testing.T) {
	h := NewHost(HostConfig{Pairing: testHostIdentity(t), Addr: "127.0.0.1:0", Password: "123456", Rotate: config.RotateNever}, nil)
	defer h.Close()
	if err := h.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	h.SetAutoAccept(false)
	v, err := dialFiles(t, h.ListenAddr(), "123456")
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	waitFor(t, "pending", func() bool { return h.State().Phase == HostPending })
	if err := h.SetEnabled(false); err != nil {
		t.Fatal(err)
	}
	h.Accept()
	waitFor(t, "request cancelled", func() bool { return h.State().Phase == HostOff })
	time.Sleep(200 * time.Millisecond)
	if p := h.State().Phase; p != HostOff {
		t.Fatalf("phase after a late accept = %v, want off", p)
	}
}

// A connection let in by its password alone is refused when that password
// changed while it was being set up.
func TestHostPasswordCurrent(t *testing.T) {
	h := NewHost(HostConfig{Pairing: testHostIdentity(t), Addr: "127.0.0.1:0", Password: "123456", Fixed: "fixed-pass-1", Rotate: config.RotateNever}, nil)
	defer h.Close()
	check := func(pw string, want bool) {
		t.Helper()
		h.mu.Lock()
		got := h.passwordCurrentLocked(pw)
		h.mu.Unlock()
		if got != want {
			t.Fatalf("passwordCurrent(%q) = %v, want %v", pw, got, want)
		}
	}
	check("123456", true)
	check("fixed-pass-1", true)
	check("", false)
	old := h.State().Password
	h.RefreshPassword()
	check(old, false)
	h.SetFixedPassword("fixed-pass-2")
	check("fixed-pass-1", false)
	check("fixed-pass-2", true)
}

// A connection that stalls in the handshake does not hold up the next one.
func TestHostStalledHandshakeDoesNotBlock(t *testing.T) {
	h := NewHost(HostConfig{Pairing: testHostIdentity(t), Addr: "127.0.0.1:0", Password: "123456", Rotate: config.RotateNever}, nil)
	defer h.Close()
	if err := h.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	h.SetAutoAccept(true)
	stalled, err := net.Dial("tcp", h.ListenAddr())
	if err != nil {
		t.Fatal(err)
	}
	defer stalled.Close() // reads the challenge, never answers
	time.Sleep(100 * time.Millisecond)
	start := time.Now()
	v, err := dialFiles(t, h.ListenAddr(), "123456")
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	if d := time.Since(start); d > 10*time.Second {
		t.Fatalf("connect took %v behind a stalled handshake", d)
	}
}

package app

import (
	"context"
	"net"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tangoui/kkdesk/internal/rendezvous"
)

func TestRemoteFileSession(t *testing.T) {
	const token = "test-enrollment-token-of-sufficient-length"
	const secret = "test-relay-secret-of-sufficient-length"
	relay, err := rendezvous.StartRelay(rendezvous.RelayConfig{
		UDPAddr: "127.0.0.1:0", BindIP: "127.0.0.1", PublicIP: net.IPv4(127, 0, 0, 1),
		MinPort: 49160, MaxPort: 49259, Secret: secret,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()
	for _, tc := range []struct {
		name      string
		relayOnly bool
	}{{"direct", false}, {"TURN", true}} {
		t.Run(tc.name, func(t *testing.T) {
			signaling, err := rendezvous.NewServer(token, secret, []string{"turn:" + relay.UDPAddr.String()})
			if err != nil {
				t.Fatal(err)
			}
			signaling.RelayOnly = tc.relayOnly
			server := httptest.NewServer(signaling)
			defer server.Close()
			cfg := rendezvous.Config{URL: server.URL, Token: token}
			h := NewHost(HostConfig{Pairing: testHostIdentity(t), Addr: "127.0.0.1:0", Password: "123456", AutoAccept: false, Remote: cfg}, nil)
			if err := h.SetEnabled(true); err != nil {
				t.Fatal(err)
			}
			defer h.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
			defer cancel()
			for h.State().RemoteCode == "" && ctx.Err() == nil {
				time.Sleep(10 * time.Millisecond)
			}
			code := h.State().RemoteCode
			if code == "" {
				t.Fatalf("device did not register: %+v", h.State())
			}
			v := NewViewer(ViewerConfig{Addr: code, Password: "123456", FilesOnly: true, Remote: cfg}, nil)
			defer v.Close()
			if err := v.Connect(ctx); err != nil {
				t.Fatal(err)
			}
			for h.State().Phase != HostPending && ctx.Err() == nil {
				time.Sleep(10 * time.Millisecond)
			}
			if state := h.State(); state.Phase != HostPending || !state.RemotePeer {
				t.Fatalf("remote request must wait for confirmation: %+v", state)
			}
			h.mu.Lock()
			servingFiles := h.fsrv != nil
			h.mu.Unlock()
			if servingFiles {
				t.Fatal("file access started before confirmation")
			}
			h.Accept()
			if _, err := v.Files().Home(ctx); err != nil {
				t.Fatalf("file request failed: %v", err)
			}
			if v.State().Relayed != tc.relayOnly {
				t.Fatalf("relayed = %v, want %v", v.State().Relayed, tc.relayOnly)
			}
		})
	}
}

// A wrong password on a cross-network connection is refused like on the
// LAN, with 密码错误, instead of falling back to a confirmation the user
// cannot tell apart from a missing password.
func TestRemoteWrongPasswordRefused(t *testing.T) {
	cfg, done := remoteTestService(t)
	defer done()
	for _, autoAccept := range []bool{true, false} {
		h := NewHost(HostConfig{Pairing: testHostIdentity(t), Addr: "127.0.0.1:0", Password: "k7Pm4x", AutoAccept: autoAccept, Remote: cfg}, nil)
		if err := h.SetEnabled(true); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		for h.State().RemoteCode == "" && ctx.Err() == nil {
			time.Sleep(10 * time.Millisecond)
		}
		v := NewViewer(ViewerConfig{Addr: h.State().RemoteCode, Password: "k7Pm4X", FilesOnly: true, Remote: cfg}, nil)
		err := v.Connect(ctx)
		if err == nil || !strings.Contains(err.Error(), "密码错误") {
			t.Fatalf("autoAccept %v: connect = %v, want 密码错误", autoAccept, err)
		}
		if p := h.State().Phase; p == HostPending || p == HostActive {
			t.Fatalf("autoAccept %v: host phase %v after a wrong password", autoAccept, p)
		}
		v.Close()
		h.Close()
		cancel()
	}
}

// A cross-network controller that gives the host's password connects
// without a confirmation when the host turned 连接前需要确认 off; a wrong
// password, or confirmation on, still asks.
// remoteTestService starts a rendezvous server with a TURN relay on
// loopback; call done when finished.
func remoteTestService(t *testing.T) (rendezvous.Config, func()) {
	t.Helper()
	const token = "test-enrollment-token-of-sufficient-length"
	const secret = "test-relay-secret-of-sufficient-length"
	relay, err := rendezvous.StartRelay(rendezvous.RelayConfig{
		UDPAddr: "127.0.0.1:0", BindIP: "127.0.0.1", PublicIP: net.IPv4(127, 0, 0, 1),
		MinPort: 49260, MaxPort: 49359, Secret: secret,
	})
	if err != nil {
		t.Fatal(err)
	}
	signaling, err := rendezvous.NewServer(token, secret, []string{"turn:" + relay.UDPAddr.String()})
	if err != nil {
		relay.Close()
		t.Fatal(err)
	}
	server := httptest.NewServer(signaling)
	return rendezvous.Config{URL: server.URL, Token: token}, func() { server.Close(); relay.Close() }
}

func TestRemotePasswordSkipsConfirmation(t *testing.T) {
	cfg, done := remoteTestService(t)
	defer done()
	for _, tc := range []struct {
		name       string
		autoAccept bool
		password   string
		confirm    bool
	}{
		{"right temporary password", true, "k7Pm4x", false},
		{"right fixed password", true, "fixed-pass-1", false},

		{"confirmation on", false, "k7Pm4x", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := NewHost(HostConfig{Pairing: testHostIdentity(t), Addr: "127.0.0.1:0", Password: "k7Pm4x", Fixed: "fixed-pass-1", AutoAccept: tc.autoAccept, Remote: cfg}, nil)
			if err := h.SetEnabled(true); err != nil {
				t.Fatal(err)
			}
			defer h.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
			defer cancel()
			for h.State().RemoteCode == "" && ctx.Err() == nil {
				time.Sleep(10 * time.Millisecond)
			}
			v := NewViewer(ViewerConfig{Addr: h.State().RemoteCode, Password: tc.password, FilesOnly: true, Remote: cfg}, nil)
			defer v.Close()
			if err := v.Connect(ctx); err != nil {
				t.Fatal(err)
			}
			for ctx.Err() == nil {
				if p := h.State().Phase; p == HostPending || p == HostActive {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if pending := h.State().Phase == HostPending; pending != tc.confirm {
				t.Fatalf("asks for confirmation: %v, want %v (%+v)", pending, tc.confirm, h.State())
			}
			if !tc.confirm {
				if _, err := v.Files().Home(ctx); err != nil {
					t.Fatalf("file request failed: %v", err)
				}
			}
		})
	}
}

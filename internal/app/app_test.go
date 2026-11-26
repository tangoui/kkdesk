package app

import (
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"syscall"
	"testing"

	"github.com/tangoui/kkdesk/internal/session"
)

// Temporary passwords are 6 characters mixing digits, upper- and lowercase
// letters, without the look-alikes 0 O o 1 l I.
func TestNewPassword(t *testing.T) {
	seen := map[string]bool{}
	for range 1000 {
		pw := NewPassword()
		if len(pw) != 6 {
			t.Fatalf("NewPassword() = %q, want 6 characters", pw)
		}
		if strings.ContainsAny(pw, "0Oo1lI") {
			t.Fatalf("NewPassword() = %q has a look-alike character", pw)
		}
		if strings.Trim(pw, passwordAlphabet) != "" {
			t.Fatalf("NewPassword() = %q outside the alphabet", pw)
		}
		if !strings.ContainsAny(pw, "23456789") || !strings.ContainsAny(pw, "ABCDEFGHJKLMNPQRSTUVWXYZ") ||
			!strings.ContainsAny(pw, "abcdefghijkmnpqrstuvwxyz") {
			t.Fatalf("NewPassword() = %q does not mix digits, upper- and lowercase", pw)
		}
		seen[pw] = true
	}
	if len(seen) < 990 {
		t.Fatalf("only %d distinct passwords out of 1000", len(seen))
	}
}

func TestIsLoopback(t *testing.T) {
	for addr, want := range map[string]bool{
		"127.0.0.1:21118": true, "127.0.0.1": true, "localhost:1": true, "[::1]:21118": true,
		"192.168.1.20:21118": false, "192.168.1.20": false, "example.com": false,
	} {
		if got := IsLoopback(addr); got != want {
			t.Errorf("IsLoopback(%q) = %v, want %v", addr, got, want)
		}
	}
}

func TestNewViewerDefaults(t *testing.T) {
	v := NewViewer(ViewerConfig{Addr: "127.0.0.1"}, nil)
	if v.Addr() != "127.0.0.1:21118" {
		t.Errorf("Addr = %q, want default port", v.Addr())
	}
	if !v.ViewOnly() {
		t.Error("loopback viewer should default to view-only")
	}
	if NewViewer(ViewerConfig{Addr: "192.168.1.20:5"}, nil).ViewOnly() {
		t.Error("remote viewer should default to control")
	}
	if !NewViewer(ViewerConfig{Addr: "192.168.1.20:5", ViewOnly: true}, nil).ViewOnly() {
		t.Error("ViewOnly config should force view-only")
	}
}

func TestDialError(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{session.ErrBadPassword, "密码错误"},
		{context.DeadlineExceeded, "超时"},
		{fmt.Errorf("dial: %w", syscall.ECONNREFUSED), "没有开启远程控制"},
		{fmt.Errorf("session: rejected: x"), "连接失败"},
		{&net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connect", syscall.EHOSTUNREACH)}, "不在同一个局域网"},
		{&net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connect", syscall.ENETUNREACH)}, "不在同一个局域网"},
		{&net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connectex", syscall.Errno(10065))}, "不在同一个局域网"}, // WSAEHOSTUNREACH
	} {
		if got := DialError("h:1", tc.err); !strings.Contains(got, tc.want) {
			t.Errorf("DialError(%v) = %q, want it to contain %q", tc.err, got, tc.want)
		}
	}
}

func TestHostRejectsWhenOff(t *testing.T) {
	h := NewHost(HostConfig{Pairing: testHostIdentity(t), Addr: "127.0.0.1:0", Password: "123456"}, nil)
	if h.State().Phase != HostOff {
		t.Fatal("new host should be off")
	}
	if err := h.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	if h.State().Phase != HostListening || h.ListenAddr() == "" {
		t.Fatalf("state after enable: %+v", h.State())
	}
	old := h.State().Password
	h.RefreshPassword()
	if h.State().Password == old && h.State().Password == "123456" {
		t.Error("RefreshPassword kept the password")
	}
	h.SetEnabled(false)
	if h.State().Phase != HostOff || h.ListenAddr() != "" {
		t.Fatalf("state after disable: %+v", h.State())
	}
	h.Close()
}

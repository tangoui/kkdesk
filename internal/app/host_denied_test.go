package app

import (
	"net"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/tangoui/kkdesk/internal/pairing"
	"github.com/tangoui/kkdesk/internal/session"
)

// Windows refuses ports inside ranges reserved for Hyper-V, WSL and Docker
// with WSAEACCES (10013): the host moves to the next free port and says so.
func TestHostMovesOffAReservedPort(t *testing.T) {
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	base := probe.Addr().(*net.TCPAddr).Port
	probe.Close()
	reserved := "127.0.0.1:" + strconv.Itoa(base)
	old := listen
	listen = func(addr string, identity *pairing.Store, verify session.PasswordVerifier) (*session.Host, error) {
		if addr == reserved {
			return nil, &net.OpError{Op: "listen", Net: "tcp", Err: os.NewSyscallError("bind", syscall.Errno(10013))}
		}
		return session.Listen(addr, identity, verify)
	}
	defer func() { listen = old }()

	moved := 0
	h := NewHost(HostConfig{Pairing: testHostIdentity(t), Addr: reserved, Password: "123456", OnPortMoved: func(p int) { moved = p }}, nil)
	defer h.Close()
	if err := h.SetEnabled(true); err != nil {
		t.Fatalf("SetEnabled = %v, want a fallback port", err)
	}
	s := h.State()
	if s.Err != "" || s.Phase != HostListening {
		t.Fatalf("state: %+v", s)
	}
	if s.Port <= base || s.Port > base+altPorts {
		t.Fatalf("port %d, want one of the %d after %d", s.Port, altPorts, base)
	}
	if !strings.Contains(s.Notice, "已改用 "+strconv.Itoa(s.Port)) {
		t.Fatalf("notice %q does not name the new port %d", s.Notice, s.Port)
	}
	if moved != s.Port {
		t.Fatalf("OnPortMoved got %d, want %d (saved so the next start uses it)", moved, s.Port)
	}
}

// A Hyper-V reservation usually spans 100 ports or more, so all the ports
// right after the configured one can be refused too: the system picks a free
// port (it never hands out a reserved one), which is saved and kept across
// restarts of the listener.
func TestHostLetsTheSystemPickWhenTheWholeRangeIsReserved(t *testing.T) {
	old := listen
	listen = func(addr string, identity *pairing.Store, verify session.PasswordVerifier) (*session.Host, error) {
		if _, p, _ := net.SplitHostPort(addr); p != "0" {
			return nil, &net.OpError{Op: "listen", Net: "tcp", Err: os.NewSyscallError("bind", syscall.Errno(10013))}
		}
		return session.Listen(addr, identity, verify)
	}
	defer func() { listen = old }()

	moved := 0
	h := NewHost(HostConfig{Pairing: testHostIdentity(t), Addr: "127.0.0.1:21115", Password: "123456", OnPortMoved: func(p int) { moved = p }}, nil)
	defer h.Close()
	if err := h.SetEnabled(true); err != nil {
		t.Fatalf("SetEnabled = %v, want a system-picked port", err)
	}
	s := h.State()
	if s.Err != "" || s.Phase != HostListening || s.Port == 0 || s.Port == 21115 {
		t.Fatalf("state: %+v", s)
	}
	if moved != s.Port || !strings.Contains(s.Notice, "已改用 "+strconv.Itoa(s.Port)) {
		t.Fatalf("moved %d, notice %q, port %d", moved, s.Notice, s.Port)
	}
	// Turning the host off and on again keeps the new port.
	_ = h.SetEnabled(false)
	listen = session.Listen
	if err := h.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	if p := h.State().Port; p != s.Port {
		t.Fatalf("port after restart %d, want %d", p, s.Port)
	}
}

func TestAddrDenied(t *testing.T) {
	if !addrDenied(&net.OpError{Err: os.NewSyscallError("bind", syscall.Errno(10013))}) {
		t.Error("WSAEACCES not recognized")
	}
	if !addrDenied(&net.OpError{Err: os.NewSyscallError("bind", syscall.EACCES)}) {
		t.Error("EACCES not recognized")
	}
	if addrDenied(&net.OpError{Err: os.NewSyscallError("bind", syscall.EADDRINUSE)}) {
		t.Error("EADDRINUSE taken for denied")
	}
}

package app

import (
	"net"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"

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
	listen = func(addr string, pw ...string) (*session.Host, error) {
		if addr == reserved {
			return nil, &net.OpError{Op: "listen", Net: "tcp", Err: os.NewSyscallError("bind", syscall.Errno(10013))}
		}
		return session.Listen(addr, pw...)
	}
	defer func() { listen = old }()

	h := NewHost(HostConfig{Addr: reserved, Password: "123456"}, nil)
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

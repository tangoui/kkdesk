package app

import (
	"net"
	"strings"
	"testing"
	"time"
)

// A port held by a process that is exiting (a hot reload, a quick restart)
// frees up a moment later: the host keeps trying instead of giving up.
func TestHostRetriesWhenPortBusy(t *testing.T) {
	old := listenRetryEvery
	listenRetryEvery = 100 * time.Millisecond
	defer func() { listenRetryEvery = old }()

	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := busy.Addr().String()
	h := NewHost(HostConfig{Addr: addr, Password: "123456"}, nil)
	defer h.Close()
	if err := h.SetEnabled(true); err != nil {
		t.Fatalf("SetEnabled with a busy port = %v, want nil (retrying)", err)
	}
	if s := h.State(); !strings.Contains(s.Err, "重试") {
		t.Fatalf("state while the port is busy: %+v, want a retrying message", s)
	}
	time.Sleep(300 * time.Millisecond)
	busy.Close()
	deadline := time.Now().Add(3 * time.Second)
	for h.ListenAddr() == "" && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if h.ListenAddr() == "" {
		t.Fatalf("host not listening after the port was freed: %+v", h.State())
	}
	if s := h.State(); s.Err != "" || s.Phase != HostListening {
		t.Fatalf("state after recovery: %+v", s)
	}
}

// Turning the host off stops the retries.
func TestHostRetryStopsWhenDisabled(t *testing.T) {
	old := listenRetryEvery
	listenRetryEvery = 50 * time.Millisecond
	defer func() { listenRetryEvery = old }()

	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	h := NewHost(HostConfig{Addr: busy.Addr().String(), Password: "123456"}, nil)
	defer h.Close()
	_ = h.SetEnabled(true)
	_ = h.SetEnabled(false)
	busy.Close()
	time.Sleep(300 * time.Millisecond)
	if h.ListenAddr() != "" || h.State().Phase != HostOff {
		t.Fatalf("host listened after being turned off: %+v", h.State())
	}
}

package app

import (
	"testing"

	"github.com/tangoui/kkdesk/internal/config"
)

func TestSessionsRegistry(t *testing.T) {
	h := NewHost(HostConfig{Pairing: testHostIdentity(t), Addr: "127.0.0.1:0", Password: "123456", AutoAccept: true, Rotate: config.RotateNever}, nil)
	defer h.Close()
	if err := h.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	n0 := len(Sessions())
	v, err := dialFiles(t, h.ListenAddr(), "123456")
	if err != nil {
		t.Fatal(err)
	}
	ss := Sessions()
	if len(ss) != n0+1 {
		t.Fatalf("%d sessions, want %d", len(ss), n0+1)
	}
	got := ss[len(ss)-1]
	if got.Viewer != v || !got.FilesOnly || got.Since.IsZero() || got.Addr != h.ListenAddr() {
		t.Fatalf("session %+v", got)
	}
	closeAndWait(t, h, v)
	waitFor(t, "unregistered", func() bool { return len(Sessions()) == n0 })
}

func TestTransferHistory(t *testing.T) {
	ClearTransferHistory()
	RecordTransfer(TransferRecord{Name: "a"})
	RecordTransfer(TransferRecord{Name: "b", Upload: true})
	h := TransferHistory()
	if len(h) != 2 || h[0].Name != "b" || h[1].Name != "a" || h[0].At.IsZero() {
		t.Fatalf("history %+v", h)
	}
	for range maxHistory + 5 {
		RecordTransfer(TransferRecord{Name: "x"})
	}
	if len(TransferHistory()) != maxHistory {
		t.Fatal("history not bounded")
	}
	ClearTransferHistory()
	if len(TransferHistory()) != 0 {
		t.Fatal("not cleared")
	}
}

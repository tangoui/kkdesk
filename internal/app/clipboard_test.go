package app

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/tangoui/kkdesk/internal/session"
)

type memBoard struct {
	mu   sync.Mutex
	text string
}

func (b *memBoard) ReadText() (string, error) { b.mu.Lock(); defer b.mu.Unlock(); return b.text, nil }
func (b *memBoard) WriteText(s string) error  { b.mu.Lock(); b.text = s; b.mu.Unlock(); return nil }
func (b *memBoard) get() string               { s, _ := b.ReadText(); return s }

// peers connects a controller to a host over loopback.
func peers(t *testing.T) (hp, cp *session.Peer) {
	t.Helper()
	var gate session.PasswordGate
	ln, err := session.Listen("127.0.0.1:0", testHostIdentity(t), func(source string, tx session.PasswordTranscript, proof []byte) (string, time.Duration) {
		return gate.Check(time.Now(), source, tx, proof, "123456", "")
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	acc := make(chan *session.Peer, 1)
	go func() { p, _ := ln.Accept(ctx); acc <- p }()
	cp, err = session.Dial(ctx, ln.Addr().String(), "123456")
	if err != nil {
		t.Fatal(err)
	}
	hp = <-acc
	if hp == nil {
		t.Fatal("accept failed")
	}
	t.Cleanup(func() { cp.Close(); hp.Close() })
	return hp, cp
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out: %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// A copy on either side reaches the other one; the host's 剪贴板 switch
// and a view-only controller stop it.
func TestClipboardSync(t *testing.T) {
	hp, cp := peers(t)
	hb, vb := &memBoard{text: "host before"}, &memBoard{text: "viewer before"}
	h := NewHost(HostConfig{Pairing: testHostIdentity(t), Addr: "127.0.0.1:0", Password: "123456", Clipboard: hb}, nil)
	defer h.Close()
	v := NewViewer(ViewerConfig{Addr: "192.0.2.1:21118", Clipboard: vb}, nil)
	defer h.syncClipboard(hp)()
	defer v.syncClipboard(cp)()

	time.Sleep(700 * time.Millisecond) // a poll or two: nothing from before the session moves
	if hb.get() != "host before" || vb.get() != "viewer before" {
		t.Fatalf("clipboards changed at start: host %q viewer %q", hb.get(), vb.get())
	}

	vb.WriteText("from the viewer")
	eventually(t, "viewer → host", func() bool { return hb.get() == "from the viewer" })
	hb.WriteText("from the host")
	eventually(t, "host → viewer", func() bool { return vb.get() == "from the host" })

	// 剪贴板 off on the host: neither direction.
	h.SetClipboardAccess(false)
	if h.State().ClipboardAccess {
		t.Fatal("state still shows clipboard access")
	}
	vb.WriteText("blocked v")
	hb.WriteText("blocked h")
	time.Sleep(1200 * time.Millisecond)
	if hb.get() != "blocked h" || vb.get() != "blocked v" {
		t.Fatalf("synced while off: host %q viewer %q", hb.get(), vb.get())
	}

	// On again, but the controller is view-only: it neither sends nor
	// applies.
	h.SetClipboardAccess(true)
	v.SetViewOnly(true)
	hb.WriteText("host while view-only")
	vb.WriteText("viewer while view-only")
	time.Sleep(1200 * time.Millisecond)
	if hb.get() != "host while view-only" || vb.get() != "viewer while view-only" {
		t.Fatalf("synced while view-only: host %q viewer %q", hb.get(), vb.get())
	}
}

// In 传文件 mode the controller does not sync its clipboard.
func TestClipboardSyncFilesOnly(t *testing.T) {
	hp, cp := peers(t)
	hb, vb := &memBoard{}, &memBoard{}
	h := NewHost(HostConfig{Pairing: testHostIdentity(t), Addr: "127.0.0.1:0", Password: "123456", Clipboard: hb}, nil)
	defer h.Close()
	defer h.syncClipboard(hp)()
	v := NewViewer(ViewerConfig{Addr: "192.0.2.1:21118", FilesOnly: true, Clipboard: vb}, nil)
	defer v.syncClipboard(cp)()
	time.Sleep(300 * time.Millisecond)
	vb.WriteText("viewer")
	time.Sleep(1200 * time.Millisecond)
	if hb.get() != "" {
		t.Fatalf("files-only controller synced %q", hb.get())
	}
}

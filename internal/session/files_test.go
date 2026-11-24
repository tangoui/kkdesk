package session

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"mmdesk/internal/files"
)

// connectMode is connect with dial options.
func connectMode(t *testing.T, opts DialOptions) (host, ctl *Peer) {
	t.Helper()
	h, err := Listen("127.0.0.1:0", testPassword)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	acc := make(chan *Peer, 1)
	errc := make(chan error, 1)
	go func() {
		p, err := h.Accept(ctx)
		errc <- err
		acc <- p
	}()
	ctl, err = DialWithOptions(ctx, h.Addr().String(), testPassword, opts)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	if err := <-errc; err != nil {
		t.Fatalf("Accept: %v", err)
	}
	host = <-acc
	t.Cleanup(func() { ctl.Close(); host.Close() })
	return host, ctl
}

func TestDialModes(t *testing.T) {
	host, ctl := connect(t)
	if host.Mode() != ModeDesktop || ctl.Mode() != ModeDesktop {
		t.Errorf("default mode: host %q ctl %q", host.Mode(), ctl.Mode())
	}
	host, ctl = connectMode(t, DialOptions{Mode: ModeFiles})
	if host.Mode() != ModeFiles || ctl.Mode() != ModeFiles {
		t.Errorf("files mode: host %q ctl %q", host.Mode(), ctl.Mode())
	}
	if err := host.WriteVideo([]byte{0, 0, 0, 1, 0x65}, time.Now(), true); err == nil {
		t.Error("WriteVideo succeeded in files-only mode")
	}
}

// TestFilesOverPeer moves 50 MB each way over a real loopback connection.
func TestFilesOverPeer(t *testing.T) {
	host, ctl := connectMode(t, DialOptions{Mode: ModeFiles})
	c := files.NewClient(ctl.FileChannel())
	c.SetLogf(t.Logf)
	// The host attaches its server late; early requests must not be lost.
	homeErr := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, err := c.Home(ctx)
		homeErr <- err
	}()
	time.Sleep(200 * time.Millisecond)
	s := files.NewServer(host.FileChannel(), nil)
	s.SetLogf(t.Logf)
	if err := <-homeErr; err != nil {
		t.Fatalf("Home before server attached: %v", err)
	}

	src, dst := t.TempDir(), t.TempDir()
	const size = 50 << 20
	data := make([]byte, size)
	for i := range data {
		data[i] = byte(i*7 + i>>13)
	}
	if err := os.WriteFile(filepath.Join(src, "big.bin"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	for _, dir := range []string{"download", "upload"} {
		var tr *files.Transfer
		var err error
		out := filepath.Join(dst, dir)
		os.Mkdir(out, 0o755)
		t0 := time.Now()
		if dir == "download" {
			tr, err = c.Download(ctx, filepath.Join(src, "big.bin"), out, nil)
		} else {
			tr, err = c.Upload(ctx, filepath.Join(src, "big.bin"), out, nil)
		}
		if err != nil {
			t.Fatal(err)
		}
		res, err := tr.Wait()
		if err != nil {
			t.Fatalf("%s: %v", dir, err)
		}
		el := time.Since(t0)
		t.Logf("%s 50 MB over a loopback Peer: %v, %.1f MB/s", dir, el.Round(time.Millisecond), float64(res.Bytes)/el.Seconds()/1e6)
		got, err := os.ReadFile(filepath.Join(out, "big.bin"))
		if err != nil || !bytes.Equal(got, data) {
			t.Fatalf("%s: content mismatch (%v)", dir, err)
		}
	}

	// Closing the session fails the client promptly.
	ctl.Close()
	select {
	case <-c.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("client not closed after the peer closed")
	}
	select {
	case <-s.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("server not closed after the peer closed")
	}
}

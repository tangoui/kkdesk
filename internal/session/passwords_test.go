package session

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

// tryDial dials h with pw while one Accept runs, and returns both errors.
func tryDial(t *testing.T, h *Host, pw string) (dialErr, acceptErr error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	type res struct {
		p   *Peer
		err error
	}
	acc := make(chan res, 1)
	go func() {
		p, err := h.Accept(ctx)
		acc <- res{p, err}
	}()
	ctl, err := Dial(ctx, h.Addr().String(), pw)
	r := <-acc
	// Close only when both sides are done: closing one early fails the
	// other's setup.
	if ctl != nil {
		ctl.Close()
	}
	if r.p != nil {
		r.p.Close()
	}
	return err, r.err
}

func TestTemporaryAndFixedPasswords(t *testing.T) {
	h, err := Listen("127.0.0.1:0", "123456", "fixed-pass-1")
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	for _, pw := range []string{"123456", "fixed-pass-1"} {
		if d, a := tryDial(t, h, pw); d != nil || a != nil {
			t.Fatalf("password %q: dial %v, accept %v", pw, d, a)
		}
	}
	if d, a := tryDial(t, h, "wrong"); !errors.Is(d, ErrBadPassword) || !errors.Is(a, ErrBadPassword) {
		t.Fatalf("wrong password: dial %v, accept %v", d, a)
	}
}

func TestSetPasswordsInvalidatesOld(t *testing.T) {
	h, err := Listen("127.0.0.1:0", "123456", "fixed-pass-1")
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	h.SetPasswords("123456", "fixed-pass-2")
	if d, a := tryDial(t, h, "fixed-pass-1"); !errors.Is(d, ErrBadPassword) || !errors.Is(a, ErrBadPassword) {
		t.Fatalf("old fixed password: dial %v, accept %v", d, a)
	}
	for _, pw := range []string{"123456", "fixed-pass-2"} {
		if d, a := tryDial(t, h, pw); d != nil || a != nil {
			t.Fatalf("password %q: dial %v, accept %v", pw, d, a)
		}
	}
	// Clearing the fixed password leaves only the temporary one.
	h.SetPasswords("654321", "")
	if d, _ := tryDial(t, h, "fixed-pass-2"); !errors.Is(d, ErrBadPassword) {
		t.Fatalf("cleared fixed password: dial %v", d)
	}
	if d, _ := tryDial(t, h, ""); !errors.Is(d, ErrBadPassword) {
		t.Fatalf("empty password must never match: dial %v", d)
	}
	if d, a := tryDial(t, h, "654321"); d != nil || a != nil {
		t.Fatalf("new temporary password: dial %v, accept %v", d, a)
	}
}

// A port probe (connect, close) is reported as ErrProbe, not as an error
// worth logging.
func TestProbeConnection(t *testing.T) {
	h, err := Listen("127.0.0.1:0", "123456")
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	acc := make(chan error, 1)
	go func() {
		_, err := h.Accept(ctx)
		acc <- err
	}()
	c, err := net.Dial("tcp", h.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	if err := <-acc; !errors.Is(err, ErrProbe) {
		t.Fatalf("Accept after a probe: got %v, want ErrProbe", err)
	}
}

// A quality request that arrives before the host registered OnQuality
// (the host's user was still confirming) is delivered on registration.
func TestQualityBeforeOnQuality(t *testing.T) {
	host, ctl := connect(t)
	want := Quality{BitrateBps: 3_000_000, FPS: 30}
	if err := ctl.SendQuality(want); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		host.mu.Lock()
		pending := host.pendingQuality
		host.mu.Unlock()
		if pending != nil || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	got := make(chan Quality, 1)
	host.OnQuality(func(q Quality) { got <- q })
	select {
	case q := <-got:
		if q != want {
			t.Fatalf("got %+v, want %+v", q, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("pending quality not delivered")
	}
}


// The accepted peer remembers which password it proved, so the app can
// refuse it if that password changed before the session was let in.
func TestPeerRemembersItsPassword(t *testing.T) {
	h, err := Listen("127.0.0.1:0", "123456", "fixed-pass-1")
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	for _, pw := range []string{"123456", "fixed-pass-1"} {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		acc := make(chan *Peer, 1)
		go func() {
			p, _ := h.Accept(ctx)
			acc <- p
		}()
		ctl, err := Dial(ctx, h.Addr().String(), pw)
		p := <-acc
		if err != nil || p == nil {
			cancel()
			t.Fatalf("password %q: dial %v, peer %v", pw, err, p)
		}
		if got := p.AuthPassword(); got != pw {
			t.Errorf("AuthPassword = %q, want %q", got, pw)
		}
		ctl.Close()
		p.Close()
		cancel()
	}
}

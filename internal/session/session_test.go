package session

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/tangoui/kkdesk/internal/input"
)

const testPassword = "123456"

// connect starts a Host on loopback and dials it, returning both peers.
func connect(t *testing.T) (host, ctl *Peer) {
	t.Helper()
	h, err := Listen("127.0.0.1:0", testPassword)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.Close() })

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
	ctl, err = Dial(ctx, h.Addr().String(), testPassword)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	r := <-acc
	if r.err != nil {
		t.Fatalf("Accept: %v", r.err)
	}
	t.Cleanup(func() { ctl.Close(); r.p.Close() })
	return r.p, ctl
}

func TestBadPasswordThenGood(t *testing.T) {
	h, err := Listen("127.0.0.1:0", testPassword)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	accErr := make(chan error, 2)
	accPeer := make(chan *Peer, 1)
	go func() {
		_, err := h.Accept(ctx)
		accErr <- err
		p, err := h.Accept(ctx)
		accErr <- err
		accPeer <- p
	}()

	if _, err := Dial(ctx, h.Addr().String(), "000000"); !errors.Is(err, ErrBadPassword) {
		t.Fatalf("Dial with wrong password: got %v, want ErrBadPassword", err)
	}
	if err := <-accErr; !errors.Is(err, ErrBadPassword) {
		t.Fatalf("Accept after wrong password: got %v, want ErrBadPassword", err)
	}

	ctl, err := Dial(ctx, h.Addr().String(), testPassword)
	if err != nil {
		t.Fatalf("Dial with right password: %v", err)
	}
	defer ctl.Close()
	if err := <-accErr; err != nil {
		t.Fatalf("second Accept: %v", err)
	}
	host := <-accPeer
	defer host.Close()
}

// annexB joins NAL units with 4-byte start codes.
func annexB(nals ...[]byte) []byte {
	var b []byte
	for _, n := range nals {
		b = append(b, 0, 0, 0, 1)
		b = append(b, n...)
	}
	return b
}

func nal(header byte, size int, seed byte) []byte {
	n := make([]byte, size)
	n[0] = header
	for i := 1; i < size; i++ {
		// Avoid accidental start codes: never emit zero bytes.
		n[i] = byte(i)*7 + seed | 1
	}
	return n
}

func syntheticFrames() [][]byte {
	sps := []byte{0x67, 0x42, 0xe0, 0x1f, 0x8c, 0x8d, 0x40, 0x50, 0x1e, 0xd0}
	pps := []byte{0x68, 0xce, 0x3c, 0x80}
	var frames [][]byte
	frames = append(frames, annexB(sps, pps, nal(0x65, 5000, 1)))
	for i := 0; i < 8; i++ {
		size := 300
		if i%3 == 1 {
			size = 5000
		}
		frames = append(frames, annexB(nal(0x41, size, byte(i+2))))
	}
	frames = append(frames, annexB(sps, pps, nal(0x65, 800, 99)))
	return frames
}

type gotFrame struct {
	data []byte
	at   time.Time
}

func TestVideoAndInput(t *testing.T) {
	host, ctl := connect(t)
	testVideoAndInput(t, host, ctl)
}

func testVideoAndInput(t *testing.T, host, ctl *Peer) {
	t.Helper()
	frames := syntheticFrames()
	got := make(chan gotFrame, len(frames))
	ctl.OnVideo(func(frame []byte, captureAt time.Time) {
		got <- gotFrame{append([]byte(nil), frame...), captureAt}
	})

	var mu sync.Mutex
	var hostIn, ctlIn []input.Event
	host.OnInput(func(e input.Event) { mu.Lock(); hostIn = append(hostIn, e); mu.Unlock() })
	ctl.OnInput(func(e input.Event) { mu.Lock(); ctlIn = append(ctlIn, e); mu.Unlock() })

	base := time.Now()
	var sentAt []time.Time
	for i, f := range frames {
		at := base.Add(time.Duration(i) * 16667 * time.Microsecond)
		sentAt = append(sentAt, at)
		if err := host.WriteVideo(f, at, f[4]&0x1f == 7); err != nil {
			t.Fatalf("WriteVideo %d: %v", i, err)
		}
		time.Sleep(5 * time.Millisecond)
	}
	for i := range frames {
		select {
		case g := <-got:
			if !bytes.Equal(g.data, frames[i]) {
				t.Fatalf("frame %d: got %d bytes, want %d (content mismatch)", i, len(g.data), len(frames[i]))
			}
			if g.at.UnixNano() != sentAt[i].UnixNano() {
				t.Fatalf("frame %d: captureAt %v, want %v", i, g.at, sentAt[i])
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out waiting for frame %d", i)
		}
	}

	var wantHost, wantCtl []input.Event
	for i := 0; i < 20; i++ {
		var e input.Event
		switch i % 4 {
		case 0:
			e = input.Event{Kind: input.Move, X: float32(i) / 20, Y: 0.5}
		case 1:
			e = input.Event{Kind: input.Button, X: 0.1, Y: 0.2, Button: 1, Down: i%8 == 1}
		case 2:
			e = input.Event{Kind: input.Key, HID: uint16(4 + i), Down: true, Mods: input.ModShift}
		case 3:
			e = input.Event{Kind: input.Wheel, DX: 0, DY: float32(-i)}
		}
		if err := ctl.SendInput(e); err != nil {
			t.Fatalf("ctl SendInput: %v", err)
		}
		wantHost = append(wantHost, e)
		if i%5 == 0 {
			r := input.Event{Kind: input.Key, HID: uint16(0x28 + i), Down: false}
			if err := host.SendInput(r); err != nil {
				t.Fatalf("host SendInput: %v", err)
			}
			wantCtl = append(wantCtl, r)
		}
		time.Sleep(2 * time.Millisecond)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		n, m := len(hostIn), len(ctlIn)
		mu.Unlock()
		if n >= len(wantHost) && m >= len(wantCtl) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("inputs: host got %d/%d, ctl got %d/%d", n, len(wantHost), m, len(wantCtl))
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	// Reliable events must arrive in order; moves (unordered channel) are
	// checked separately, also in order on loopback.
	split := func(es []input.Event) (moves, rel []string) {
		for _, e := range es {
			s := fmt.Sprintf("%+v", e)
			if e.Kind == input.Move {
				moves = append(moves, s)
			} else {
				rel = append(rel, s)
			}
		}
		return
	}
	gm, gr := split(hostIn)
	wm, wr := split(wantHost)
	if fmt.Sprint(gr) != fmt.Sprint(wr) {
		t.Fatalf("host reliable inputs:\n got %v\nwant %v", gr, wr)
	}
	if fmt.Sprint(gm) != fmt.Sprint(wm) {
		t.Fatalf("host move inputs:\n got %v\nwant %v", gm, wm)
	}
	if fmt.Sprint(ctlIn) != fmt.Sprint(wantCtl) {
		t.Fatalf("ctl inputs:\n got %v\nwant %v", ctlIn, wantCtl)
	}

	st := host.Stats()
	if st.FramesSent != uint64(len(frames)) || st.BytesSent == 0 {
		t.Fatalf("host stats: %+v", st)
	}
	cs := ctl.Stats()
	if cs.FramesReceived != uint64(len(frames)) || cs.BytesReceived == 0 {
		t.Fatalf("ctl stats: %+v", cs)
	}
}

func TestKeyframeRequest(t *testing.T) {
	host, ctl := connect(t)
	req := make(chan struct{}, 4)
	host.OnKeyframeRequest(func() {
		select {
		case req <- struct{}{}:
		default:
		}
	})
	if err := ctl.RequestKeyframe(); err != nil {
		t.Fatalf("RequestKeyframe: %v", err)
	}
	select {
	case <-req:
	case <-time.After(5 * time.Second):
		t.Fatal("OnKeyframeRequest not called within 5s")
	}
}

func TestCloseNotifiesPeer(t *testing.T) {
	for _, closer := range []string{"ctl", "host"} {
		t.Run(closer+"Closes", func(t *testing.T) {
			host, ctl := connect(t)
			a, b := ctl, host
			if closer == "host" {
				a, b = host, ctl
			}
			if err := a.Close(); err != nil {
				t.Fatalf("Close: %v", err)
			}
			if err := a.Close(); err != nil {
				t.Fatalf("second Close: %v", err)
			}
			select {
			case <-a.Done():
			default:
				t.Fatal("closer's Done not closed after Close")
			}
			select {
			case <-b.Done():
			case <-time.After(5 * time.Second):
				t.Fatal("other side's Done not closed within 5s")
			}
		})
	}
}

func TestQualityAndCloseReason(t *testing.T) {
	host, ctl := connect(t)
	got := make(chan Quality, 1)
	host.OnQuality(func(q Quality) { got <- q })
	want := Quality{BitrateBps: 14_000_000, FPS: 90}
	if err := ctl.SendQuality(want); err != nil {
		t.Fatalf("SendQuality: %v", err)
	}
	select {
	case q := <-got:
		if q != want {
			t.Fatalf("OnQuality got %+v, want %+v", q, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("OnQuality not called within 5s")
	}
	if host.RemoteAddr() == "" || ctl.RemoteAddr() == "" {
		t.Fatalf("RemoteAddr empty: host %q ctl %q", host.RemoteAddr(), ctl.RemoteAddr())
	}
	if err := host.CloseWithReason("rejected"); err != nil {
		t.Fatalf("CloseWithReason: %v", err)
	}
	select {
	case <-ctl.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("ctl Done not closed within 5s")
	}
	if r := ctl.Reason(); r != "rejected" {
		t.Fatalf("Reason = %q, want %q", r, "rejected")
	}
}

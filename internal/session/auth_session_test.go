package session

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/pion/webrtc/v4"

	"github.com/tangoui/kkdesk/internal/input"
)

func TestLANV2GreetingAndSignedAnswer(t *testing.T) {
	v, identity := readAuthVector(t)
	var guard PasswordGate
	host, err := Listen("127.0.0.1:0", identity, func(source, _ string, tx PasswordTranscript, proof []byte) (string, time.Duration) {
		return guard.Check(time.Now(), source, tx, proof, testPassword, "")
	})
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	accepted := make(chan error, 1)
	go func() {
		p, err := host.Accept(ctx)
		if p != nil {
			p.Close()
		}
		accepted <- err
	}()
	conn, err := net.Dial("tcp", host.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	sc, closeSignal := newSigConn(ctx, conn)
	defer closeSignal()
	var greeting struct {
		Version int    `json:"version"`
		HostKey string `json:"hostKey"`
	}
	// The greeting names the host's key (for the first-connection check).
	if err := sc.dec.Decode(&greeting); err != nil || greeting.Version != 2 || greeting.HostKey != identity.PublicKey() {
		t.Fatalf("greeting = %+v, %v", greeting, err)
	}
	controller, err := newPeer(false, true, ModeFiles)
	if err != nil {
		t.Fatal(err)
	}
	defer controller.Close()
	offer, err := controller.offer()
	if err != nil {
		t.Fatal(err)
	}
	if err := sc.send(sigMsg{SDP: offer, Mode: ModeFiles}); err != nil {
		t.Fatal(err)
	}
	msg, err := sc.recv()
	if err != nil || msg.SDP == nil || msg.SDP.Type != webrtc.SDPTypeAnswer {
		t.Fatalf("answer = %+v, %v", msg, err)
	}
	if msg.HostKey != v.HostKey || msg.HostSig != identity.SignHostAnswer(offer.SDP, msg.SDP.SDP) {
		t.Fatal("answer was not signed by the host identity")
	}
	if err := controller.pc.SetRemoteDescription(*msg.SDP); err != nil {
		t.Fatal(err)
	}
	if err := controller.WaitReady(ctx); err != nil {
		t.Fatal(err)
	}
	if err := controller.sendMeta([]byte{0x41, 1}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-accepted:
		t.Fatalf("host returned before receiving a valid auth frame: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	// No valid proof arrives. Listener cancellation releases the gated peer.
	cancel()
	if err := <-accepted; !errors.Is(err, context.Canceled) {
		t.Fatalf("accept returned %v on cancellation", err)
	}
}

func TestLANRejectsLegacyGreeting(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			defer conn.Close()
			_ = json.NewEncoder(conn).Encode(map[string]string{"nonce": "legacy"})
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if peer, err := Dial(ctx, listener.Addr().String(), testPassword); !errors.Is(err, ErrHostSignature) {
		if peer != nil {
			peer.Close()
		}
		t.Fatalf("legacy greeting: %v", err)
	}
}

func listenForTest(t *testing.T, address, temporary, fixed string) (*Host, error) {
	t.Helper()
	_, identity := readAuthVector(t)
	var gate PasswordGate
	return Listen(address, identity, func(source, _ string, tx PasswordTranscript, proof []byte) (string, time.Duration) {
		return gate.Check(time.Now(), source, tx, proof, temporary, fixed)
	})
}

func TestPeerDropsApplicationTrafficBeforePassword(t *testing.T) {
	host, err := newPeer(true, true, ModeFiles)
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close()
	host.requirePassword()
	ctl, err := newPeer(false, true, ModeFiles)
	if err != nil {
		t.Fatal(err)
	}
	defer ctl.Close()
	offer, err := ctl.offer()
	if err != nil {
		t.Fatal(err)
	}
	answer, err := host.answer(*offer)
	if err != nil {
		t.Fatal(err)
	}
	if err := ctl.pc.SetRemoteDescription(*answer); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, peer := range []*Peer{host, ctl} {
		if err := peer.WaitReady(ctx); err != nil {
			t.Fatal(err)
		}
	}
	inputs := make(chan input.Event, 1)
	host.OnInput(func(event input.Event) { inputs <- event })
	files := make(chan struct{}, 1)
	host.FileChannel().OnMessage(func(bool, []byte) { files <- struct{}{} })
	clipboard := make(chan string, 1)
	host.OnClipboard(func(text string) { clipboard <- text })
	if err := ctl.SendClipboard("unverified"); err != nil {
		t.Fatal(err)
	}
	if err := ctl.SendInput(input.Event{Kind: input.Move, X: .5, Y: .5}); err != nil {
		t.Fatal(err)
	}
	if err := ctl.FileChannel().SendText("unverified"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-inputs:
		t.Fatal("input dispatched before password validation")
	case <-files:
		t.Fatal("file message dispatched before password validation")
	case <-clipboard:
		t.Fatal("clipboard dispatched before password validation")
	case <-time.After(150 * time.Millisecond):
	}
}

func TestPasswordPeerRequiresFourDistinctChannels(t *testing.T) {
	host, err := newPeer(true, true, ModeFiles)
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close()
	host.requirePassword()
	ctl, err := newPeer(false, true, ModeFiles)
	if err != nil {
		t.Fatal(err)
	}
	defer ctl.Close()

	opened := make(chan struct{}, 4)
	for _, label := range []string{labelMeta, labelMeta, labelMeta, labelMeta} {
		channel, err := ctl.pc.CreateDataChannel(label, nil)
		if err != nil {
			t.Fatal(err)
		}
		channel.OnOpen(func() { opened <- struct{}{} })
	}
	offer, err := ctl.pc.CreateOffer(nil)
	if err != nil {
		t.Fatal(err)
	}
	local, err := ctl.setLocalAndGather(offer)
	if err != nil {
		t.Fatal(err)
	}
	answer, err := host.answer(*local)
	if err != nil {
		t.Fatal(err)
	}
	if err := ctl.pc.SetRemoteDescription(*answer); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for range 4 {
		select {
		case <-opened:
		case <-ctx.Done():
			t.Fatal("data channels did not open")
		}
	}
	select {
	case <-host.ready:
		t.Fatal("duplicate channels satisfied the password peer's readiness")
	case <-time.After(150 * time.Millisecond):
	}
}

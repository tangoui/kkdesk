package session

import (
	"testing"
	"time"

	"github.com/pion/webrtc/v4"
)

func expectPrivacy(t *testing.T, got <-chan PrivacyMessage, want PrivacyMessage) {
	t.Helper()
	select {
	case m := <-got:
		if m != want {
			t.Fatalf("got %+v, want %+v", m, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %+v", want)
	}
}

func TestPrivacyMetaRoundTrip(t *testing.T) {
	host, viewer := connect(t)
	requests, replies := make(chan PrivacyMessage, 1), make(chan PrivacyMessage, 1)
	host.OnPrivacy(func(m PrivacyMessage) { requests <- m })
	viewer.OnPrivacy(func(m PrivacyMessage) { replies <- m })
	for _, m := range []PrivacyMessage{
		{Kind: PrivacyRequest, Status: PrivacyOn},
		{Kind: PrivacyRequest, Status: PrivacyOff},
		{Kind: LockOnDisconnectRequest, Status: PrivacyOn},
		{Kind: LockNowRequest},
	} {
		if err := viewer.SendPrivacy(m); err != nil {
			t.Fatal(err)
		}
		expectPrivacy(t, requests, m)
	}
	for _, m := range []PrivacyMessage{
		{Kind: PrivacyState, Status: PrivacyWaiting},
		{Kind: PrivacyState, Status: PrivacyNotAllowed},
		{Kind: PrivacyState, Status: PrivacyLocalExit},
		{Kind: LockOnDisconnectState, Status: PrivacyOff},
		{Kind: LockNowResult, Status: PrivacyFailed, Detail: "锁屏失败"},
		{Kind: LockNowResult, Status: PrivacyOn},
	} {
		if err := host.SendPrivacy(m); err != nil {
			t.Fatal(err)
		}
		expectPrivacy(t, replies, m)
	}
	viewer.Close()
	select {
	case <-host.Done():
		if !host.ReceivedBye() || host.Reason() != "" {
			t.Fatalf("empty bye lost: reason %q, bye %v", host.Reason(), host.ReceivedBye())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("host did not close")
	}
}

func TestPrivacyMetaIgnoresFramesOutsideTheGate(t *testing.T) {
	p := &Peer{isHost: true}
	b := []byte{byte(PrivacyRequest), byte(PrivacyOn)}
	delivered := 0
	p.handleMeta(webrtc.DataChannelMessage{Data: b})
	p.OnPrivacy(func(PrivacyMessage) { delivered++ })
	p.requirePassword()
	p.handleMeta(webrtc.DataChannelMessage{Data: b})
	p.auth.Load().verified.Store(true)
	p.handleMeta(webrtc.DataChannelMessage{Data: []byte{byte(PrivacyState), byte(PrivacyOn)}})
	p.handleMeta(webrtc.DataChannelMessage{Data: append(b, 0)})
	p.handleMeta(webrtc.DataChannelMessage{Data: b, IsString: true})
	if delivered != 0 {
		t.Fatalf("delivered %d messages outside the gate", delivered)
	}
	p.handleMeta(webrtc.DataChannelMessage{Data: b})
	if delivered != 1 {
		t.Fatalf("valid request was not delivered exactly once: %d", delivered)
	}
}

func TestPrivacyMetaFilesSession(t *testing.T) {
	host, viewer := connectMode(t, DialOptions{Mode: ModeFiles})
	host.OnPrivacy(func(m PrivacyMessage) { t.Errorf("files host received %+v", m) })
	replies := make(chan PrivacyMessage, 1)
	viewer.OnPrivacy(func(m PrivacyMessage) { replies <- m })
	for _, pairs := range []struct{ request, reply PrivacyKind }{
		{PrivacyRequest, PrivacyState}, {LockOnDisconnectRequest, LockOnDisconnectState}, {LockNowRequest, LockNowResult},
	} {
		req := PrivacyMessage{Kind: pairs.request, Status: PrivacyOn}
		if req.Kind == LockNowRequest {
			req.Status = PrivacyOff
		}
		if err := viewer.SendPrivacy(req); err != nil {
			t.Fatal(err)
		}
		expectPrivacy(t, replies, PrivacyMessage{Kind: pairs.reply, Status: PrivacyUnsupported, Detail: "仅传文件会话不支持隐私屏或锁屏"})
	}
}

package rendezvous

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tangoui/kkdesk/internal/pairing"
)

func TestMobileRelayAndPresence(t *testing.T) {
	broker, err := NewServer(strings.Repeat("a", 32), strings.Repeat("b", 32), []string{"turn:127.0.0.1:3478"})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(broker)
	defer server.Close()
	cfg := Config{URL: server.URL, Token: strings.Repeat("a", 32)}
	identity, err := pairing.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if online, err := MobileOnline(ctx, cfg, identity.ID()); err != nil || online {
		t.Fatalf("before register: online=%v, %v", online, err)
	}
	host, err := RegisterMobile(ctx, cfg, identity)
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close()
	go host.Serve(ctx, func(ctx context.Context, exchange MobileExchange) {
		request, err := exchange.Receive(ctx)
		if err == nil {
			_ = exchange.Send(ctx, pairing.Message{Type: "result", Status: request.Purpose})
		}
	})
	if online, err := MobileOnline(ctx, cfg, identity.ID()); err != nil || !online {
		t.Fatalf("after register: online=%v, %v", online, err)
	}
	guest, err := OpenMobile(ctx, cfg, identity.ID(), false)
	if err != nil {
		t.Fatal(err)
	}
	defer guest.Close()
	if err := guest.Send(ctx, pairing.Message{Type: "hello", Purpose: "pair"}); err != nil {
		t.Fatal(err)
	}
	if result, err := guest.Receive(ctx); err != nil || result.Type != "result" || result.Status != "pair" {
		t.Fatalf("relay response: %+v, %v", result, err)
	}
}

func TestMobileConcurrentCancellation(t *testing.T) {
	const token = "test-signal-secret-of-sufficient-length"
	broker, err := NewServer(token, "test-turn-secret-of-sufficient-length", []string{"turn:127.0.0.1:3478"})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(broker)
	defer server.Close()
	cfg := Config{URL: server.URL, Token: token}
	identity, err := pairing.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	host, err := RegisterMobile(ctx, cfg, identity)
	if err != nil {
		t.Fatal(err)
	}
	served := make(chan error, 1)
	defer func() {
		host.Close()
		<-served
	}()
	cancelledSend := make(chan error, 1)
	go func() {
		served <- host.Serve(ctx, func(call context.Context, ex MobileExchange) {
			request, err := ex.Receive(call)
			if err != nil {
				return
			}
			if err := ex.Send(call, pairing.Message{Type: "started", Status: request.Purpose}); err != nil {
				return
			}
			if request.Purpose == "cancel" {
				<-call.Done()
				cancelledSend <- ex.Send(call, pairing.Message{Type: "late"})
				return
			}
			request, err = ex.Receive(call)
			if err == nil {
				_ = ex.Send(call, pairing.Message{Type: "result", Status: request.Purpose})
			}
		})
	}()
	first, err := OpenMobile(ctx, cfg, identity.ID(), false)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	other, err := OpenMobile(ctx, cfg, identity.ID(), false)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	for _, request := range []struct {
		guest   *MobileGuest
		purpose string
	}{{first, "cancel"}, {other, "finish"}} {
		if err := request.guest.Send(ctx, pairing.Message{Type: "hello", Purpose: request.purpose}); err != nil {
			t.Fatal(err)
		}
		if started, err := request.guest.Receive(ctx); err != nil || started.Type != "started" || started.Status != request.purpose {
			t.Fatalf("request %s did not start: %+v, %v", request.purpose, started, err)
		}
	}
	first.Close()
	select {
	case err := <-cancelledSend:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("late send should return request cancellation: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("closing the first guest did not cancel its desktop handler")
	}
	if err := other.Send(ctx, pairing.Message{Type: "continue", Purpose: "finish"}); err != nil {
		t.Fatal(err)
	}
	if result, err := other.Receive(ctx); err != nil || result.Type != "result" || result.Status != "finish" {
		t.Fatalf("relay disconnected after another guest closed: %+v, %v", result, err)
	}
	if online, err := MobileOnline(ctx, cfg, identity.ID()); err != nil || !online {
		t.Fatalf("after forwarding: online=%v, %v", online, err)
	}
}

func TestMobileReceivePreservesReceiptBeforeEnd(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	ex := &HostMobileExchange{input: make(chan pairing.Message, 1)}
	ex.input <- pairing.Message{Type: "pair_received"}
	cancel()
	if message, err := ex.Receive(ctx); err != nil || message.Type != "pair_received" {
		t.Fatalf("end discarded the receipt queued before it: %+v, %v", message, err)
	}
	if _, err := ex.Receive(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("after the receipt: %v", err)
	}
}

func TestMobileFinalReplyRequiresGuestWrite(t *testing.T) {
	broker, err := NewServer(strings.Repeat("a", 32), strings.Repeat("b", 32), []string{"turn:127.0.0.1:3478"})
	if err != nil {
		t.Fatal(err)
	}
	reject := &atomic.Bool{}
	rejected := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		broker.ServeHTTP(rejectFinalWriter{ResponseWriter: w, reject: reject, rejected: rejected}, r)
	}))
	defer server.Close()
	cfg := Config{URL: server.URL, Token: strings.Repeat("a", 32)}
	identity, err := pairing.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	host, err := RegisterMobile(ctx, cfg, identity)
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close()
	replies := make(chan error, 1)
	go host.Serve(ctx, func(call context.Context, ex MobileExchange) {
		if _, err := ex.Receive(call); err != nil {
			return
		}
		replies <- ex.Send(call, pairing.Message{Type: "result", Status: "committed"})
	})
	guest, err := OpenMobile(ctx, cfg, identity.ID(), false)
	if err != nil {
		t.Fatal(err)
	}
	defer guest.Close()
	if err := guest.Send(ctx, pairing.Message{Type: "request"}); err != nil {
		t.Fatal(err)
	}
	if reply, err := guest.Receive(ctx); err != nil || reply.Status != "committed" {
		t.Fatalf("guest commit reply: %+v, %v", reply, err)
	}
	select {
	case err := <-replies:
		if err != nil {
			t.Fatalf("delivery acknowledgement: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("missing guest delivery acknowledgement")
	}
	guest.Close()
	failing, err := OpenMobile(ctx, cfg, identity.ID(), false)
	if err != nil {
		t.Fatal(err)
	}
	defer failing.Close()
	reject.Store(true)
	if err := failing.Send(ctx, pairing.Message{Type: "request"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-rejected:
	case <-ctx.Done():
		t.Fatal("relay never attempted the guest write")
	}
	select {
	case err := <-replies:
		if err == nil {
			t.Fatal("final reply was acknowledged after the guest write failed")
		}
	case <-ctx.Done():
		t.Fatal("host did not learn the guest write failed")
	}
	if online, err := MobileOnline(ctx, cfg, identity.ID()); err != nil || !online {
		t.Fatalf("guest write failure dropped host: %v, %v", online, err)
	}
}

type rejectFinalWriter struct {
	http.ResponseWriter
	reject   *atomic.Bool
	rejected chan<- struct{}
}

func (w rejectFinalWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, rw, err := w.ResponseWriter.(http.Hijacker).Hijack()
	if err != nil {
		return nil, nil, err
	}
	rw.Writer.Reset(rejectFinalFrame{Writer: conn, reject: w.reject, rejected: w.rejected})
	return conn, rw, nil
}

type rejectFinalFrame struct {
	io.Writer
	reject   *atomic.Bool
	rejected chan<- struct{}
}

func (w rejectFinalFrame) Write(data []byte) (int, error) {
	if bytes.Contains(data, []byte(`"status":"committed"`)) && w.reject.CompareAndSwap(true, false) {
		w.rejected <- struct{}{}
		return 0, io.ErrClosedPipe
	}
	return w.Writer.Write(data)
}

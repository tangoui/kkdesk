package app

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"net"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pion/webrtc/v4"

	"github.com/tangoui/kkdesk/internal/pairing"
	"github.com/tangoui/kkdesk/internal/rendezvous"
	"github.com/tangoui/kkdesk/internal/session"
)

func TestPhonePairingEndToEnd(t *testing.T) {
	for _, route := range []string{"lan", "server", "relay-only"} {
		t.Run(route, func(t *testing.T) {
			identity, err := pairing.Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
			defer cancel()
			cfg := rendezvous.Config{}
			if route != "lan" {
				cfg = mobileTestServer(t, route == "relay-only")
			}
			host := NewHost(HostConfig{Addr: "127.0.0.1:0", Pairing: identity, Remote: cfg, AutoAccept: true}, nil)
			defer host.Close()
			if err := host.SetEnabled(true); err != nil {
				t.Fatal(err)
			}
			if route != "lan" {
				waitPhoneTest(t, ctx, func() bool {
					online, _ := rendezvous.MobileOnline(ctx, cfg, identity.ID())
					return online
				})
			}
			uri, _, err := host.PairingCode(time.Now())
			if err != nil {
				t.Fatal(err)
			}
			qr, err := url.Parse(uri)
			if err != nil {
				t.Fatal(err)
			}
			target := qr.Query()
			open := func(media bool) (rendezvous.MobileExchange, func(), webrtc.Configuration) {
				t.Helper()
				if route == "lan" {
					ex, err := session.OpenMobileLAN(ctx, target.Get("lan"))
					if err != nil {
						t.Fatal(err)
					}
					return ex, ex.Close, webrtc.Configuration{}
				}
				ex, err := rendezvous.OpenMobile(ctx, cfg, target.Get("id"), media)
				if err != nil {
					t.Fatal(err)
				}
				ice := webrtc.Configuration{ICEServers: ex.ICE}
				if ex.RelayOnly {
					ice.ICETransportPolicy = webrtc.ICETransportPolicyRelay
				}
				return ex, ex.Close, ice
			}
			pub, key, _ := ed25519.GenerateKey(nil)
			authenticate := func(ex rendezvous.MobileExchange, purpose string) (pairing.Transcript, bool) {
				t.Helper()
				hello := pairing.Message{Type: "hello", Purpose: purpose, PublicKey: pairing.Encode(pub), Nonce: pairing.Random()}
				if err := ex.Send(ctx, hello); err != nil {
					t.Fatal(err)
				}
				challenge, err := ex.Receive(ctx)
				transcript := pairing.Transcript{Purpose: purpose, ClientNonce: hello.Nonce, HostNonce: challenge.Nonce,
					ClientKey: hello.PublicKey, HostKey: challenge.PublicKey}
				hostKey, _ := pairing.PublicKey(challenge.PublicKey)
				if err != nil || challenge.Type != "challenge" || pairing.Fingerprint(hostKey) != target.Get("fp") ||
					!transcript.Verify(challenge.PublicKey, challenge) {
					t.Fatalf("unauthenticated desktop challenge: %+v, %v", challenge, err)
				}
				return transcript, challenge.Bypass
			}
			send := func(ex rendezvous.MobileExchange, tx pairing.Transcript, msg pairing.Message) {
				t.Helper()
				tx.Sign(key, &msg)
				if err := ex.Send(ctx, msg); err != nil {
					t.Fatal(err)
				}
			}
			receive := func(ex rendezvous.MobileExchange, tx pairing.Transcript) pairing.Message {
				t.Helper()
				msg, err := ex.Receive(ctx)
				if err != nil || !tx.Verify(tx.HostKey, msg) {
					t.Fatalf("invalid response: %+v, %v", msg, err)
				}
				return msg
			}
			ex, closeSignal, _ := open(false)
			defer closeSignal()
			tx, _ := authenticate(ex, "pair")
			send(ex, tx, pairing.Message{Type: "request", Code: tx.CodeProof(target.Get("c")), Name: "Test iPhone"})
			pending := receive(ex, tx)
			hostKey, _ := pairing.PublicKey(tx.HostKey)
			want := pairing.VerificationCode(target.Get("c"), hostKey, pub)
			visible := host.PairingRequest()
			if pending.Status != "pending" || pending.Code != want ||
				visible == nil || visible.Name != "Test iPhone" || visible.Code != want {
				t.Fatalf("desktop verification dialog: %+v; wire: %+v", visible, pending)
			}
			host.DecidePairing(visible.ID, true, false)
			approved := receive(ex, tx)
			if approved.Status != "approved" || approved.Bypass || len(host.PairedPhones()) != 0 {
				t.Fatalf("premature pairing persistence: %+v", approved)
			}
			send(ex, tx, pairing.Message{Type: "pair_ack", Nonce: approved.Nonce})
			if result := receive(ex, tx); result.Status != "ok" || result.Nonce != approved.Nonce {
				t.Fatalf("pair response: %+v", result)
			}
			send(ex, tx, pairing.Message{Type: "pair_received", Nonce: approved.Nonce})
			if committed := receive(ex, tx); committed.Status != "committed" || committed.Nonce != approved.Nonce {
				t.Fatalf("missing commit confirmation: %+v", committed)
			}
			waitPhoneTest(t, ctx, func() bool { return host.PairingRequest() == nil })
			closeSignal()

			dial := func(bypass bool, biometric *ecdsa.PrivateKey) *webrtc.PeerConnection {
				t.Helper()
				ex, closeSignal, ice := open(true)
				defer closeSignal()
				tx, auto := authenticate(ex, "connect")
				if auto != bypass {
					t.Fatalf("challenge bypass=%v, want %v", auto, bypass)
				}
				pc, err := webrtc.NewPeerConnection(ice)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = pc.Close() })
				for _, label := range []string{"input", "input-move", "files", "meta"} {
					if _, err := pc.CreateDataChannel(label, nil); err != nil {
						t.Fatal(err)
					}
				}
				offer, err := pc.CreateOffer(nil)
				if err != nil {
					t.Fatal(err)
				}
				gather := webrtc.GatheringCompletePromise(pc)
				if err := pc.SetLocalDescription(offer); err != nil {
					t.Fatal(err)
				}
				select {
				case <-gather:
				case <-ctx.Done():
					t.Fatal("gathering mobile ICE timed out")
				}
				offerMessage := pairing.Message{Type: "offer", Mode: "files", SDP: pc.LocalDescription().SDP}
				if biometric != nil {
					offerMessage.BiometricKey = pairing.Encode(elliptic.Marshal(elliptic.P256(), biometric.X, biometric.Y))
					hash := sha256.Sum256(tx.Bytes(offerMessage))
					signature, err := ecdsa.SignASN1(rand.Reader, biometric, hash[:])
					if err != nil {
						t.Fatal(err)
					}
					offerMessage.BiometricSignature = pairing.Encode(signature)
				}
				send(ex, tx, offerMessage)
				answer := receive(ex, tx)
				if answer.Type != "answer" {
					t.Fatalf("peer answer: %+v", answer)
				}
				if err := pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: answer.SDP}); err != nil {
					t.Fatal(err)
				}
				return pc
			}

			first := dial(false, nil)
			waitPhoneTest(t, ctx, func() bool { return host.State().Phase == HostPending })
			if state := host.State(); state.Peer != "Test iPhone" || !state.PhonePeer || state.RemotePeer != (route != "lan") {
				t.Fatalf("connection request: %+v", state)
			}
			host.Accept()
			waitPhoneTest(t, ctx, func() bool { return host.State().Phase == HostActive })
			if err := first.Close(); err != nil {
				t.Fatal(err)
			}
			waitPhoneTest(t, ctx, func() bool { return host.State().Phase == HostListening })
			if err := host.UpdatePhone(pairing.Encode(pub), func(phone *pairing.Phone) { phone.Bypass = true }); err != nil {
				t.Fatal(err)
			}
			ed25519Only := dial(true, nil)
			waitPhoneTest(t, ctx, func() bool { return host.State().Phase == HostPending })
			if host.State().BiometricEnrollment {
				t.Fatal("Ed25519-only offer requested biometric enrollment")
			}
			host.Reject()
			_ = ed25519Only.Close()
			waitPhoneTest(t, ctx, func() bool { return host.State().Phase == HostListening })

			biometric, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
			if err != nil {
				t.Fatal(err)
			}
			enrollment := dial(true, biometric)
			waitPhoneTest(t, ctx, func() bool { return host.State().BiometricEnrollment })
			host.Accept()
			waitPhoneTest(t, ctx, func() bool { return host.State().Phase == HostActive })
			wantBioKey := pairing.Encode(elliptic.Marshal(elliptic.P256(), biometric.X, biometric.Y))
			if phone, _ := identity.Phone(pairing.Encode(pub)); phone.BiometricKey != wantBioKey {
				t.Fatal("confirmed enrollment did not persist")
			}
			_ = enrollment.Close()
			waitPhoneTest(t, ctx, func() bool { return host.State().Phase == HostListening })

			second := dial(true, biometric)
			waitPhoneTest(t, ctx, func() bool { return host.State().Phase == HostActive })
			if route == "relay-only" {
				pair, err := second.SCTP().Transport().ICETransport().GetSelectedCandidatePair()
				if err != nil || pair == nil || pair.Local.Typ != webrtc.ICECandidateTypeRelay {
					t.Fatalf("expected nominated TURN pair: %v, %v", pair, err)
				}
			}
			if err := host.RemovePhone(pairing.Encode(pub)); err != nil {
				t.Fatal(err)
			}
			ex, closeSignal, _ = open(true)
			defer closeSignal()
			tx, _ = authenticate(ex, "connect")
			send(ex, tx, pairing.Message{Type: "offer", Mode: "files", SDP: "v=0\r\n"})
			if result := receive(ex, tx); result.Status != "unpaired" {
				t.Fatalf("revoked phone: %+v", result)
			}
			closeSignal()

			invite := identity.Invite(time.Now())
			ex, closeSignal, _ = open(false)
			defer closeSignal()
			tx, _ = authenticate(ex, "pair")
			send(ex, tx, pairing.Message{Type: "request", Code: tx.CodeProof(invite.Code), Name: "Cancelled iPhone"})
			if result := receive(ex, tx); result.Status != "pending" {
				t.Fatalf("pairing before cancellation: %+v", result)
			}
			prompt := host.PairingRequest()
			if prompt == nil {
				t.Fatal("missing prompt")
			}
			closeSignal()
			waitPhoneTest(t, ctx, func() bool { return host.PairingRequest() == nil })
			host.DecidePairing(prompt.ID, true, true)
			if len(host.PairedPhones()) != 0 || host.State().Notice != "手机已取消" {
				t.Fatal("a cancelled request left a pairing behind or was not reported")
			}

			invite = identity.Invite(time.Now())
			ex, closeSignal, _ = open(false)
			defer closeSignal()
			tx, _ = authenticate(ex, "pair")
			send(ex, tx, pairing.Message{Type: "request", Code: tx.CodeProof(invite.Code), Name: "Lost Final Reply"})
			if result := receive(ex, tx); result.Status != "pending" {
				t.Fatalf("pairing before final reply: %+v", result)
			}
			host.DecidePairing(host.PairingRequest().ID, true, true)
			approved = receive(ex, tx)
			send(ex, tx, pairing.Message{Type: "pair_ack", Nonce: approved.Nonce})
			if result := receive(ex, tx); result.Status != "ok" || len(host.PairedPhones()) != 1 {
				t.Fatalf("expected provisional pairing: %+v", result)
			}
			closeSignal()
			waitPhoneTest(t, ctx, func() bool { return host.PairingRequest() == nil })
			if len(host.PairedPhones()) != 0 || host.State().Notice != "手机已取消" {
				t.Fatal("unacknowledged final reply did not roll back pairing")
			}
		})
	}
}

func waitPhoneTest(t *testing.T, ctx context.Context, condition func() bool) {
	t.Helper()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for !condition() {
		select {
		case <-ctx.Done():
			t.Fatal("timed out waiting for pairing state")
		case <-tick.C:
		}
	}
}

func mobileTestServer(t *testing.T, relayOnly bool) rendezvous.Config {
	t.Helper()
	const token = "test-signal-token-for-mobile-1234"
	const secret = "test-turn-secret-for-mobile-123456"
	tls, key, err := rendezvous.LoadOrCreateIdentity(filepath.Join(t.TempDir(), "server.pem"))
	if err != nil {
		t.Fatal(err)
	}
	relay, err := rendezvous.StartRelay(rendezvous.RelayConfig{
		UDPAddr: "127.0.0.1:0", BindIP: "127.0.0.1", PublicIP: net.IPv4(127, 0, 0, 1),
		MinPort: 49500, MaxPort: 49599, Secret: secret,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = relay.Close() })
	broker, err := rendezvous.NewServer(token, secret, []string{"turn:" + relay.UDPAddr.String() + "?transport=udp"})
	if err != nil {
		t.Fatal(err)
	}
	broker.RelayOnly = relayOnly
	http := httptest.NewUnstartedServer(broker)
	http.TLS = tls
	http.StartTLS()
	t.Cleanup(http.Close)
	return rendezvous.Config{URL: http.URL, PublicKey: key, Token: token}
}

func TestManualPairingAuthenticatesDesktopWithoutSendingCode(t *testing.T) {
	identity, err := pairing.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	host := NewHost(HostConfig{Addr: "127.0.0.1:0", Pairing: identity}, nil)
	defer host.Close()
	if err := host.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	if _, _, err := host.PairingCode(time.Now()); err != nil {
		t.Fatal(err)
	}
	formatted, addr := host.PairingManualCode()
	code := strings.ReplaceAll(formatted, "-", "")
	if len(formatted) != 19 || len(code) != 16 || addr != host.ListenAddr() {
		t.Fatalf("unexpected desktop manual entry: %q, %q", formatted, addr)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	pub, key, _ := ed25519.GenerateKey(nil)
	ex, err := session.OpenMobileLAN(ctx, addr)
	if err != nil {
		t.Fatal(err)
	}
	defer ex.Close()
	hello := pairing.Message{Type: "hello", Purpose: "pair_manual", PublicKey: pairing.Encode(pub), Nonce: pairing.Random()}
	if err := ex.Send(ctx, hello); err != nil {
		t.Fatal(err)
	}
	challenge, err := ex.Receive(ctx)
	tx := pairing.Transcript{Purpose: hello.Purpose, ClientNonce: hello.Nonce, HostNonce: challenge.Nonce,
		ClientKey: hello.PublicKey, HostKey: challenge.PublicKey}
	if err != nil || challenge.Code != tx.ManualHostProof(code) || !tx.Verify(challenge.PublicKey, challenge) {
		t.Fatalf("desktop manual proof: %+v, %v", challenge, err)
	}
	send := func(msg pairing.Message) {
		t.Helper()
		tx.Sign(key, &msg)
		if err := ex.Send(ctx, msg); err != nil {
			t.Fatal(err)
		}
	}
	receive := func() pairing.Message {
		t.Helper()
		msg, err := ex.Receive(ctx)
		if err != nil || !tx.Verify(tx.HostKey, msg) {
			t.Fatalf("unverified manual pairing reply: %+v, %v", msg, err)
		}
		return msg
	}
	send(pairing.Message{Type: "request", Code: tx.CodeProof(code), Name: "Manual iPhone"})
	hostKey, _ := pairing.PublicKey(tx.HostKey)
	if pending := receive(); pending.Status != "pending" || pending.Code != pairing.VerificationCode(code, hostKey, pub) {
		t.Fatalf("wrong manual verification code: %+v", pending)
	}
	host.DecidePairing(host.PairingRequest().ID, true, false)
	approved := receive()
	send(pairing.Message{Type: "pair_ack", Nonce: approved.Nonce})
	if result := receive(); result.Status != "ok" {
		t.Fatalf("manual pairing failed: %+v", result)
	}
	send(pairing.Message{Type: "pair_received", Nonce: approved.Nonce})
	if committed := receive(); committed.Status != "committed" || committed.Nonce != approved.Nonce {
		t.Fatalf("missing manual commit confirmation: %+v", committed)
	}
	waitPhoneTest(t, ctx, func() bool { return host.PairingRequest() == nil })
	if phones := host.PairedPhones(); len(phones) != 1 || phones[0].PublicKey != hello.PublicKey {
		t.Fatal("manual pairing did not persist the verified phone")
	}
}

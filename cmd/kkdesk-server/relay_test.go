package main

import (
	"context"
	"net"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/pion/webrtc/v4"
	"golang.org/x/net/proxy"

	"github.com/tangoui/kkdesk/internal/pairing"
	"github.com/tangoui/kkdesk/internal/rendezvous"
)

func TestPinnedMobileGuestTURNOverTCPRelayOnly(t *testing.T) {
	const token = "test-signal-secret-of-sufficient-length"
	const secret = "test-turn-secret-of-sufficient-length"
	identityPath := filepath.Join(t.TempDir(), "identity.pem")
	cfg, err := parseConfig([]string{
		"-tls-identity", identityPath, "-turn-public-ip", "127.0.0.1", "-turn-host", "127.0.0.1",
		"-turn-listen", "127.0.0.1:0", "-turn-tls-listen", "127.0.0.1:0", "-relay-bind", "127.0.0.1",
		"-relay-min", "49760", "-relay-max", "49859", "-relay-only",
	})
	if err != nil {
		t.Fatal(err)
	}
	tlsConfig, key, err := rendezvous.LoadOrCreateIdentity(cfg.Identity)
	if err != nil {
		t.Fatal(err)
	}
	ip := net.ParseIP(cfg.PublicIP).To4()
	relay, err := startRelay(cfg, ip, tlsConfig, secret)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { relay.Close() })
	if relay.TCPAddr == nil || relay.TCPAddr.String() != relay.UDPAddr.String() {
		t.Fatalf("default mobile TURN TCP listener %v must share UDP port %v", relay.TCPAddr, relay.UDPAddr)
	}
	broker, err := newBroker(cfg, ip, relay, token, secret)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(broker)
	server.TLS = tlsConfig
	server.StartTLS()
	defer server.Close()
	client := rendezvous.Config{URL: server.URL, Token: token, PublicKey: key}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	identity, err := pairing.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	host, err := rendezvous.RegisterMobile(ctx, client, identity)
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close()
	hostExchanges := make(chan *rendezvous.HostMobileExchange, 1)
	go host.Serve(ctx, func(call context.Context, ex rendezvous.MobileExchange) {
		hostExchanges <- ex.(*rendezvous.HostMobileExchange)
		<-call.Done()
	})
	guest, err := rendezvous.OpenMobile(ctx, client, identity.ID(), true)
	if err != nil {
		t.Fatal(err)
	}
	defer guest.Close()
	var hostEx *rendezvous.HostMobileExchange
	select {
	case hostEx = <-hostExchanges:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	udpURL := "turn:" + relay.UDPAddr.String() + "?transport=udp"
	tcpURL := "turn:" + relay.TCPAddr.String() + "?transport=tcp"
	tlsURL := "turns:" + relay.TLSAddr.String() + "?transport=tcp"
	if !slices.Equal(iceURLs(guest.ICE), []string{udpURL, tcpURL}) || !guest.RelayOnly {
		t.Fatalf("unexpected mobile guest ICE URLs or policy: %v, relay-only=%v", iceURLs(guest.ICE), guest.RelayOnly)
	}
	desktopURLs := []string{"stun:" + relay.UDPAddr.String(), udpURL, tlsURL}
	if !slices.Equal(iceURLs(hostEx.ICE), desktopURLs) || !hostEx.RelayOnly {
		t.Fatalf("unexpected desktop host ICE URLs or policy: %v, relay-only=%v", iceURLs(hostEx.ICE), hostEx.RelayOnly)
	}
	code := rendezvous.NewCode()
	desktop, err := rendezvous.Register(ctx, client, code)
	if err != nil {
		t.Fatal(err)
	}
	defer desktop.CloseNow()
	desktopGuest, err := rendezvous.Join(ctx, client, code)
	if err != nil {
		t.Fatal(err)
	}
	defer desktopGuest.Close()
	if !slices.Equal(iceURLs(desktopGuest.ICE), desktopURLs) {
		t.Fatalf("unexpected desktop guest ICE URLs: %v", iceURLs(desktopGuest.ICE))
	}

	// Restrict both ends to their advertised TCP transport and use the broker's credentials.
	dialer, err := client.TURNProxy()
	if err != nil {
		t.Fatal(err)
	}
	sender := newTCPRelayPeer(t, guest.ICE, nil)
	receiver := newTCPRelayPeer(t, hostEx.ICE, dialer)
	received := make(chan string, 1)
	receiver.OnDataChannel(func(dc *webrtc.DataChannel) {
		dc.OnMessage(func(msg webrtc.DataChannelMessage) { received <- string(msg.Data) })
	})
	dc, err := sender.CreateDataChannel("relay", nil)
	if err != nil {
		t.Fatal(err)
	}
	opened := make(chan struct{})
	dc.OnOpen(func() { close(opened) })
	offer, err := sender.CreateOffer(nil)
	if err != nil {
		t.Fatal(err)
	}
	setLocalAndGather(t, ctx, sender, offer)
	if err := receiver.SetRemoteDescription(*sender.LocalDescription()); err != nil {
		t.Fatal(err)
	}
	answer, err := receiver.CreateAnswer(nil)
	if err != nil {
		t.Fatal(err)
	}
	setLocalAndGather(t, ctx, receiver, answer)
	if err := sender.SetRemoteDescription(*receiver.LocalDescription()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-opened:
	case <-ctx.Done():
		t.Fatal("TURN TCP data channel did not open")
	}
	const payload = "kkdesk relay over TCP"
	if err := dc.SendText(payload); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-received:
		if got != payload {
			t.Fatalf("relayed data = %q, want %q", got, payload)
		}
	case <-ctx.Done():
		t.Fatal("TURN TCP did not deliver data")
	}
	ice := sender.SCTP().Transport().ICETransport()
	pair, err := ice.GetSelectedCandidatePair()
	if err != nil || pair == nil || pair.Local.Typ != webrtc.ICECandidateTypeRelay || pair.Remote.Typ != webrtc.ICECandidateTypeRelay {
		t.Fatalf("expected a relay pair, got %v, %v", pair, err)
	}
	for _, endpoint := range []struct {
		peer *webrtc.PeerConnection
		want string
	}{{sender, "tcp"}, {receiver, "tls"}} {
		ice := endpoint.peer.SCTP().Transport().ICETransport()
		pair, ok := ice.GetSelectedCandidatePairStats()
		candidate, found := endpoint.peer.GetStats()[pair.LocalCandidateID].(webrtc.ICECandidateStats)
		if !ok || !found || candidate.RelayProtocol != endpoint.want {
			t.Fatalf("expected selected relay candidate using %s, got %+v", endpoint.want, candidate)
		}
	}
}

func TestMobileTURNTCPListenOverride(t *testing.T) {
	cfg, err := parseConfig([]string{
		"-tls-identity", "identity.pem", "-turn-public-ip", "127.0.0.1",
		"-turn-listen", "0.0.0.0:0", "-turn-tcp-listen", "127.0.0.1:0",
	})
	if err != nil {
		t.Fatal(err)
	}
	relay, err := startRelay(cfg, net.ParseIP(cfg.PublicIP).To4(), nil, "test-turn-secret-of-sufficient-length")
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()
	if addr, ok := relay.TCPAddr.(*net.TCPAddr); !ok || !addr.IP.IsLoopback() {
		t.Fatalf("TURN TCP listener did not use the configured address: %v", relay.TCPAddr)
	}
}

func TestMobileTURNTCPAvoidsConfiguredTCPPorts(t *testing.T) {
	for _, cfg := range []serverConfig{
		{TURNListen: "127.0.0.1:3478", TURNTLSListen: ":3478", Listen: ":3479"},
		{TURNListen: "127.0.0.1:3478", TURNTLSListen: ":3479", Listen: ":3478"},
	} {
		if got := defaultMobileTURNTCPListen(cfg); got != "127.0.0.1:3480" {
			t.Fatalf("default TURN TCP address %q overlaps configured ports: %+v", got, cfg)
		}
	}
}

func iceURLs(servers []webrtc.ICEServer) []string {
	var urls []string
	for _, server := range servers {
		urls = append(urls, server.URLs...)
	}
	return urls
}

func newTCPRelayPeer(t *testing.T, servers []webrtc.ICEServer, dialer proxy.Dialer) *webrtc.PeerConnection {
	t.Helper()
	var tcp []webrtc.ICEServer
	for _, server := range servers {
		for _, url := range server.URLs {
			if strings.HasSuffix(url, "?transport=tcp") {
				server.URLs = []string{url}
				tcp = append(tcp, server)
			}
		}
	}
	var settings webrtc.SettingEngine
	settings.SetNetworkTypes([]webrtc.NetworkType{webrtc.NetworkTypeUDP4, webrtc.NetworkTypeTCP4})
	if dialer != nil {
		settings.SetICEProxyDialer(dialer)
	}
	peer, err := webrtc.NewAPI(webrtc.WithSettingEngine(settings)).NewPeerConnection(webrtc.Configuration{
		ICEServers: tcp, ICETransportPolicy: webrtc.ICETransportPolicyRelay,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { peer.Close() })
	return peer
}

func setLocalAndGather(t *testing.T, ctx context.Context, peer *webrtc.PeerConnection, desc webrtc.SessionDescription) {
	t.Helper()
	gathered := webrtc.GatheringCompletePromise(peer)
	if err := peer.SetLocalDescription(desc); err != nil {
		t.Fatal(err)
	}
	select {
	case <-gathered:
		if sdp := peer.LocalDescription(); sdp == nil || !strings.Contains(sdp.SDP, " typ relay") {
			t.Fatal("peer did not gather a TCP relay candidate")
		}
	case <-ctx.Done():
		t.Fatal("failed to gather TCP relay candidates")
	}
}

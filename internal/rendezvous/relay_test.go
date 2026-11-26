package rendezvous

import (
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/pion/turn/v4"
)

func TestTURNOverStream(t *testing.T) {
	t.Run("TLS", func(t *testing.T) { testTURNOverStream(t, true) })
	t.Run("TCP", func(t *testing.T) { testTURNOverStream(t, false) })
}

func testTURNOverStream(t *testing.T, secure bool) {
	t.Helper()
	const secret = "test-turn-secret-of-sufficient-length"
	cfg := RelayConfig{
		UDPAddr: "127.0.0.1:0",
		BindIP:  "127.0.0.1", PublicIP: net.IPv4(127, 0, 0, 1),
		MinPort: 49460, MaxPort: 49559, Secret: secret,
	}
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	dial := dialer.Dial
	if secure {
		certServer := httptest.NewTLSServer(http.NotFoundHandler())
		roots := x509.NewCertPool()
		roots.AddCert(certServer.Certificate())
		cfg.TLSAddr, cfg.TLS = "127.0.0.1:0", certServer.TLS.Clone()
		certServer.Close()
		clientTLS := &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
		dial = (&tls.Dialer{NetDialer: dialer, Config: clientTLS}).Dial
	} else {
		cfg.TCPAddr = "127.0.0.1:0"
	}
	relay, err := StartRelay(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()
	listener := relay.TCPAddr
	if secure {
		listener = relay.TLSAddr
	}
	addr := listener.String()
	conn, err := dial("tcp4", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	user, pass, err := turn.GenerateLongTermTURNRESTCredentials(secret, "test", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	client, err := turn.NewClient(&turn.ClientConfig{
		TURNServerAddr: addr, Conn: turn.NewSTUNConn(conn), Username: user, Password: pass,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err := client.Listen(); err != nil {
		t.Fatal(err)
	}
	allocated, err := client.Allocate()
	if err != nil {
		t.Fatal(err)
	}
	defer allocated.Close()
	dst, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()
	const payload = "relay to UDP"
	if _, err := allocated.WriteTo([]byte(payload), dst.LocalAddr()); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 64)
	if err := dst.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	n, from, err := dst.ReadFrom(buf)
	if err != nil || string(buf[:n]) != payload || from.String() != allocated.LocalAddr().String() {
		t.Fatalf("relayed UDP packet %q from %v, %v", buf[:n], from, err)
	}
}

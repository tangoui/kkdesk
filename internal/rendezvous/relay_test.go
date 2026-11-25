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

func TestTURNOverTLS(t *testing.T) {
	const secret = "test-turn-secret-of-sufficient-length"
	certServer := httptest.NewTLSServer(http.NotFoundHandler())
	roots := x509.NewCertPool()
	roots.AddCert(certServer.Certificate())
	serverTLS := certServer.TLS.Clone()
	certServer.Close()

	relay, err := StartRelay(RelayConfig{
		UDPAddr: "127.0.0.1:0", TLSAddr: "127.0.0.1:0", TLS: serverTLS,
		BindIP: "127.0.0.1", PublicIP: net.IPv4(127, 0, 0, 1),
		MinPort: 49460, MaxPort: 49559, Secret: secret,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()
	addr := relay.TLSAddr.String()
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 5 * time.Second}, "tcp4", addr, &tls.Config{
		RootCAs: roots, MinVersion: tls.VersionTLS12,
	})
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
	if _, err := allocated.WriteTo([]byte("relay over TLS"), dst.LocalAddr()); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 64)
	if err := dst.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	n, _, err := dst.ReadFrom(buf)
	if err != nil || string(buf[:n]) != "relay over TLS" {
		t.Fatalf("relayed packet %q, %v", buf[:n], err)
	}
}

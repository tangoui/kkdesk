// kkdesk-server provides device rendezvous and TURN for kkdesk.
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/tangoui/kkdesk/internal/rendezvous"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	signalAddr := flag.String("listen", "127.0.0.1:8080", "signaling listen address")
	cert := flag.String("tls-cert", "", "TLS certificate PEM including chain")
	key := flag.String("tls-key", "", "TLS private key PEM")
	turnAddr := flag.String("turn-listen", "0.0.0.0:3478", "TURN UDP listen address")
	turnTLS := flag.String("turn-tls-listen", "", "optional TURN TLS listen address")
	turnHost := flag.String("turn-host", "", "public DNS name used by TURN TLS")
	publicIP := flag.String("turn-public-ip", "", "public IPv4 advertised for TURN allocations")
	relayBind := flag.String("relay-bind", "0.0.0.0", "local IPv4 to bind relay allocations")
	minPort := flag.Int("relay-min", 49160, "first UDP relay port")
	maxPort := flag.Int("relay-max", 49259, "last UDP relay port")
	relayOnly := flag.Bool("relay-only", false, "require media to use the relay")
	flag.Parse()

	token, secret := os.Getenv("KKDESK_SERVER_TOKEN"), os.Getenv("KKDESK_TURN_SECRET")
	if len(token) < 32 || len(secret) < 32 || token == secret {
		return errors.New("set distinct KKDESK_SERVER_TOKEN and KKDESK_TURN_SECRET values of at least 32 characters")
	}
	ip := net.ParseIP(*publicIP).To4()
	if ip == nil || *minPort < 1024 || *maxPort < *minPort || *maxPort > 65535 {
		return errors.New("provide -turn-public-ip and a valid UDP relay port range")
	}
	var tlsConfig *tls.Config
	if *cert != "" || *key != "" {
		pair, err := tls.LoadX509KeyPair(*cert, *key)
		if err != nil {
			return err
		}
		tlsConfig = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{pair}}
	} else {
		host, _, err := net.SplitHostPort(*signalAddr)
		if err != nil || (host != "localhost" && !net.ParseIP(host).IsLoopback()) {
			return errors.New("signaling without TLS must listen on loopback for local testing or a TLS reverse proxy")
		}
	}
	if *turnTLS != "" && (tlsConfig == nil || *turnHost == "") {
		return errors.New("TURN TLS requires -tls-cert, -tls-key and -turn-host")
	}
	relay, err := rendezvous.StartRelay(rendezvous.RelayConfig{
		UDPAddr: *turnAddr, TLSAddr: *turnTLS, TLS: tlsConfig,
		BindIP: *relayBind, PublicIP: ip, MinPort: uint16(*minPort), MaxPort: uint16(*maxPort), Secret: secret,
	})
	if err != nil {
		return fmt.Errorf("start TURN: %w", err)
	}
	defer relay.Close()
	udp := net.JoinHostPort(ip.String(), strconv.Itoa(relay.UDPAddr.(*net.UDPAddr).Port))
	urls := []string{"stun:" + udp, "turn:" + udp + "?transport=udp"}
	if relay.TLSAddr != nil {
		tcp := net.JoinHostPort(*turnHost, strconv.Itoa(relay.TLSAddr.(*net.TCPAddr).Port))
		urls = append(urls, "turns:"+tcp+"?transport=tcp")
	}
	broker, err := rendezvous.NewServer(token, secret, urls)
	if err != nil {
		return err
	}
	broker.RelayOnly = *relayOnly
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	server := &http.Server{
		Addr: *signalAddr, Handler: broker, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second,
		TLSConfig: tlsConfig, BaseContext: func(net.Listener) context.Context { return ctx },
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	log.Printf("kkdesk signaling on %s, TURN UDP on %s, relay range %d–%d", *signalAddr, relay.UDPAddr, *minPort, *maxPort)
	if tlsConfig != nil {
		err = server.ListenAndServeTLS("", "")
	} else {
		err = server.ListenAndServe()
	}
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

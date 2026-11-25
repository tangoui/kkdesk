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
	if err := run(os.Args[1:]); err != nil && !errors.Is(err, flag.ErrHelp) {
		log.Fatal(err)
	}
}

func run(args []string) error {
	cfg, err := parseConfig(args)
	if err != nil {
		return err
	}

	token, secret := os.Getenv("KKDESK_SERVER_TOKEN"), os.Getenv("KKDESK_TURN_SECRET")
	if len(token) < 32 || len(secret) < 32 || token == secret {
		return errors.New("set distinct KKDESK_SERVER_TOKEN and KKDESK_TURN_SECRET values of at least 32 characters")
	}
	ip := net.ParseIP(cfg.PublicIP).To4()
	if ip == nil || cfg.MinPort < 1024 || cfg.MaxPort < cfg.MinPort || cfg.MaxPort > 65535 {
		return errors.New("provide -turn-public-ip and a valid UDP relay port range")
	}
	var tlsConfig *tls.Config
	switch {
	case cfg.Identity != "":
		if cfg.Cert != "" || cfg.Key != "" {
			return errors.New("tls-identity cannot be combined with tls-cert or tls-key")
		}
		if cfg.TURNHost == "" {
			cfg.TURNHost = ip.String()
		}
		if !ip.Equal(net.ParseIP(cfg.TURNHost)) {
			return errors.New("TURN TLS must use the public IP with tls-identity")
		}
		var publicKey string
		tlsConfig, publicKey, err = rendezvous.LoadOrCreateIdentity(cfg.Identity)
		if err != nil {
			return fmt.Errorf("load TLS identity: %w", err)
		}
		log.Printf("kkdesk server public key: %s", publicKey)
	case cfg.Cert != "" || cfg.Key != "":
		pair, err := tls.LoadX509KeyPair(cfg.Cert, cfg.Key)
		if err != nil {
			return err
		}
		tlsConfig = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{pair}}
	default:
		host, _, err := net.SplitHostPort(cfg.Listen)
		if err != nil || (host != "localhost" && !net.ParseIP(host).IsLoopback()) {
			return errors.New("signaling without TLS must listen on loopback for local testing or a TLS reverse proxy")
		}
	}
	if cfg.TURNTLSListen != "" && (tlsConfig == nil || cfg.TURNHost == "") {
		return errors.New("TURN TLS requires -tls-cert, -tls-key and -turn-host")
	}
	relay, err := startRelay(cfg, ip, tlsConfig, secret)
	if err != nil {
		return fmt.Errorf("start TURN: %w", err)
	}
	defer relay.Close()
	broker, err := newBroker(cfg, ip, relay, token, secret)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	server := &http.Server{
		Addr: cfg.Listen, Handler: broker, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second,
		TLSConfig: tlsConfig, BaseContext: func(net.Listener) context.Context { return ctx },
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	log.Printf("kkdesk signaling on %s, TURN UDP on %s, relay range %d–%d", cfg.Listen, relay.UDPAddr, cfg.MinPort, cfg.MaxPort)
	if relay.TCPAddr != nil {
		log.Printf("kkdesk mobile TURN TCP on %s", relay.TCPAddr)
	}
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

func startRelay(cfg serverConfig, ip net.IP, tlsConfig *tls.Config, secret string) (*rendezvous.Relay, error) {
	if cfg.TURNListen == "" {
		cfg.TURNListen = ":0"
	}
	tcpListen := cfg.TURNTCPListen
	if cfg.Identity != "" && tcpListen == "" {
		tcpListen = defaultMobileTURNTCPListen(cfg)
	}
	return rendezvous.StartRelay(rendezvous.RelayConfig{
		UDPAddr: cfg.TURNListen, TCPAddr: tcpListen, TLSAddr: cfg.TURNTLSListen, TLS: tlsConfig,
		BindIP: cfg.RelayBind, PublicIP: ip, MinPort: uint16(cfg.MinPort), MaxPort: uint16(cfg.MaxPort), Secret: secret,
	})
}

func defaultMobileTURNTCPListen(cfg serverConfig) string {
	host, port, _ := net.SplitHostPort(cfg.TURNListen)
	udpPort, _ := net.LookupPort("udp", port)
	signalingPort := tcpListenerPort(cfg.Listen)
	tlsPort := tcpListenerPort(cfg.TURNTLSListen)
	if udpPort == 0 || (udpPort != signalingPort && udpPort != tlsPort) {
		return cfg.TURNListen
	}
	// Keep the existing signaling and TURN TLS ports when they share the UDP port number.
	fallback := 3479
	for fallback == signalingPort || fallback == tlsPort {
		fallback++
	}
	return net.JoinHostPort(host, strconv.Itoa(fallback))
}

func tcpListenerPort(address string) int {
	_, port, _ := net.SplitHostPort(address)
	n, _ := net.LookupPort("tcp", port)
	return n
}

func newBroker(cfg serverConfig, ip net.IP, relay *rendezvous.Relay, token, secret string) (*rendezvous.Server, error) {
	udp := net.JoinHostPort(ip.String(), strconv.Itoa(relay.UDPAddr.(*net.UDPAddr).Port))
	turnUDP := "turn:" + udp + "?transport=udp"
	urls := []string{"stun:" + udp, turnUDP}
	if relay.TLSAddr != nil {
		tlsAddr := net.JoinHostPort(cfg.TURNHost, strconv.Itoa(relay.TLSAddr.(*net.TCPAddr).Port))
		urls = append(urls, "turns:"+tlsAddr+"?transport=tcp")
	}
	opts := rendezvous.ServerOptions{MaxConnections: cfg.MaxConnections, CredentialTTL: time.Duration(cfg.CredentialTTL)}
	if relay.TCPAddr != nil {
		tcp := net.JoinHostPort(ip.String(), strconv.Itoa(relay.TCPAddr.(*net.TCPAddr).Port))
		opts.MobileGuestTURNURLs = []string{turnUDP, "turn:" + tcp + "?transport=tcp"}
	}
	broker, err := rendezvous.NewServerWithOptions(token, secret, urls, opts)
	if err != nil {
		return nil, err
	}
	broker.RelayOnly = cfg.RelayOnly
	return broker, nil
}

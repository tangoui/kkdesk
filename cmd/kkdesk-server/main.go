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
	relay, err := rendezvous.StartRelay(rendezvous.RelayConfig{
		UDPAddr: cfg.TURNListen, TLSAddr: cfg.TURNTLSListen, TLS: tlsConfig,
		BindIP: cfg.RelayBind, PublicIP: ip, MinPort: uint16(cfg.MinPort), MaxPort: uint16(cfg.MaxPort), Secret: secret,
	})
	if err != nil {
		return fmt.Errorf("start TURN: %w", err)
	}
	defer relay.Close()
	udp := net.JoinHostPort(ip.String(), strconv.Itoa(relay.UDPAddr.(*net.UDPAddr).Port))
	urls := []string{"stun:" + udp, "turn:" + udp + "?transport=udp"}
	if relay.TLSAddr != nil {
		tcp := net.JoinHostPort(cfg.TURNHost, strconv.Itoa(relay.TLSAddr.(*net.TCPAddr).Port))
		urls = append(urls, "turns:"+tcp+"?transport=tcp")
	}
	broker, err := rendezvous.NewServerWithOptions(token, secret, urls, rendezvous.ServerOptions{
		MaxConnections: cfg.MaxConnections, CredentialTTL: time.Duration(cfg.CredentialTTL),
	})
	if err != nil {
		return err
	}
	broker.RelayOnly = cfg.RelayOnly
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

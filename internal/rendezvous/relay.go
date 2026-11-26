package rendezvous

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sync"

	"github.com/pion/turn/v4"
)

type RelayConfig struct {
	UDPAddr  string
	TCPAddr  string // optional; the same address as UDPAddr shares its port
	TLSAddr  string
	TLS      *tls.Config
	BindIP   string
	PublicIP net.IP
	MinPort  uint16
	MaxPort  uint16
	Secret   string
}

type Relay struct {
	*turn.Server
	UDPAddr net.Addr
	TCPAddr net.Addr
	TLSAddr net.Addr
}

func StartRelay(cfg RelayConfig) (*Relay, error) {
	if len(cfg.Secret) < 32 || cfg.PublicIP.To4() == nil ||
		cfg.MinPort == 0 || cfg.MinPort > cfg.MaxPort || cfg.BindIP == "" ||
		(cfg.TLSAddr != "" && cfg.TLS == nil) {
		return nil, errors.New("invalid TURN configuration")
	}
	udp, err := net.ListenPacket("udp4", cfg.UDPAddr)
	if err != nil {
		return nil, err
	}
	gen := func() *turn.RelayAddressGeneratorPortRange {
		return &turn.RelayAddressGeneratorPortRange{
			RelayAddress: cfg.PublicIP, Address: cfg.BindIP,
			MinPort: cfg.MinPort, MaxPort: cfg.MaxPort,
		}
	}
	permission := func(_ net.Addr, peer net.IP) bool {
		if cfg.PublicIP.IsLoopback() {
			return peer.IsLoopback()
		}
		return publicPeer(peer)
	}
	const realm = "kkdesk"
	var mu sync.Mutex
	allocations := make(map[string]int)
	settings := turn.ServerConfig{
		Realm: realm, AuthHandler: turn.LongTermTURNRESTAuthHandler(cfg.Secret, nil),
		PacketConnConfigs: []turn.PacketConnConfig{{
			PacketConn: udp, RelayAddressGenerator: gen(), PermissionHandler: permission,
		}},
		QuotaHandler: func(user, _ string, _ net.Addr) bool {
			mu.Lock()
			defer mu.Unlock()
			return allocations[user] < 4
		},
		EventHandler: turn.EventHandler{
			OnAllocationCreated: func(_, _ net.Addr, _, user, _ string, _ net.Addr, _ int) {
				mu.Lock()
				allocations[user]++
				mu.Unlock()
			},
			OnAllocationDeleted: func(_, _ net.Addr, _, user, _ string) {
				mu.Lock()
				if allocations[user] <= 1 {
					delete(allocations, user)
				} else {
					allocations[user]--
				}
				mu.Unlock()
			},
		},
	}
	relay := &Relay{UDPAddr: udp.LocalAddr()}
	defer func() {
		if relay.Server == nil {
			udp.Close()
			for _, cfg := range settings.ListenerConfigs {
				cfg.Listener.Close()
			}
		}
	}()
	addListener := func(stream net.Listener) {
		// TURN over TCP or TLS still allocates a UDP relay socket.
		settings.ListenerConfigs = append(settings.ListenerConfigs, turn.ListenerConfig{
			Listener: stream, RelayAddressGenerator: gen(), PermissionHandler: permission,
		})
	}
	if cfg.TCPAddr != "" {
		addr := cfg.TCPAddr
		if addr == cfg.UDPAddr {
			addr = relay.UDPAddr.String()
		}
		stream, err := net.Listen("tcp4", addr)
		if err != nil {
			return nil, fmt.Errorf("listen for TURN TCP: %w", err)
		}
		relay.TCPAddr = stream.Addr()
		addListener(stream)
	}
	if cfg.TLSAddr != "" {
		stream, err := tls.Listen("tcp4", cfg.TLSAddr, cfg.TLS)
		if err != nil {
			return nil, fmt.Errorf("listen for TURN TLS: %w", err)
		}
		relay.TLSAddr = stream.Addr()
		addListener(stream)
	}
	relay.Server, err = turn.NewServer(settings)
	if err != nil {
		return nil, err
	}
	return relay, nil
}

var carrierNAT = netip.MustParsePrefix("100.64.0.0/10")

func publicPeer(ip net.IP) bool {
	addr, ok := netip.AddrFromSlice(ip)
	addr = addr.Unmap()
	return ok && addr.Is4() && addr.IsGlobalUnicast() && !addr.IsPrivate() && !carrierNAT.Contains(addr) &&
		addr.As4()[0] != 0 && addr.As4()[0] < 240
}

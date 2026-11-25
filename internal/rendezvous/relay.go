package rendezvous

import (
	"crypto/tls"
	"errors"
	"net"
	"net/netip"
	"sync"

	"github.com/pion/turn/v4"
)

type RelayConfig struct {
	UDPAddr  string
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
	var stream net.Listener
	if cfg.TLSAddr != "" {
		stream, err = tls.Listen("tcp4", cfg.TLSAddr, cfg.TLS)
		if err != nil {
			udp.Close()
			return nil, err
		}
		relay.TLSAddr = stream.Addr()
		settings.ListenerConfigs = []turn.ListenerConfig{{
			Listener: stream, RelayAddressGenerator: gen(), PermissionHandler: permission,
		}}
	}
	relay.Server, err = turn.NewServer(settings)
	if err != nil {
		udp.Close()
		if stream != nil {
			stream.Close()
		}
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

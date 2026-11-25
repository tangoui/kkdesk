package app

import (
	"reflect"
	"testing"
)

// The address another computer can reach comes first: the one on the route
// to the internet, then other real adapters, and adapters of virtual
// machines and containers (Hyper-V's vEthernet, WSL, Docker, VMware,
// VirtualBox) last. On a Windows PC with Hyper-V, 192.168.112.1 used to be
// listed above the LAN address.
func TestRankAddrs(t *testing.T) {
	cands := []ifAddr{
		{"vEthernet (Default Switch)", "192.168.112.1"},
		{"vEthernet (WSL)", "172.29.160.1"},
		{"以太网", "192.168.1.28"},
		{"VMware Network Adapter VMnet8", "192.168.50.1"},
		{"WLAN", "10.0.0.5"},
		{"docker0", "172.17.0.1"},
	}
	got := rankAddrs(cands, "192.168.1.28")
	want := []string{"192.168.1.28", "10.0.0.5", "192.168.112.1", "172.29.160.1", "192.168.50.1", "172.17.0.1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rankAddrs = %v, want %v", got, want)
	}
	// Offline (no route): real adapters still come before virtual ones.
	got = rankAddrs(cands, "")
	want = []string{"192.168.1.28", "10.0.0.5", "192.168.112.1", "172.29.160.1", "192.168.50.1", "172.17.0.1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rankAddrs offline = %v, want %v", got, want)
	}
	// macOS bridges of virtual machines (bridge100) are virtual too.
	got = rankAddrs([]ifAddr{{"bridge100", "192.168.64.1"}, {"en0", "192.168.1.230"}}, "")
	if got[0] != "192.168.1.230" {
		t.Fatalf("bridge100 first: %v", got)
	}
}

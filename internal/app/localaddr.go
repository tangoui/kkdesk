package app

import (
	"net"
	"sort"
	"strings"
)

// ifAddr is an IPv4 address and the name of its interface.
type ifAddr struct {
	name, ip string
}

// localIPv4 lists the IPv4 addresses of the interfaces that are up
// (excluding loopback and link-local), the one another computer is most
// likely to reach first (see rankAddrs).
func localIPv4() []string {
	var cands []ifAddr
	ifs, _ := net.Interfaces()
	for _, i := range ifs {
		if i.Flags&net.FlagUp == 0 || i.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := i.Addrs()
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil && !n.IP.IsLinkLocalUnicast() {
				cands = append(cands, ifAddr{i.Name, n.IP.String()})
			}
		}
	}
	return rankAddrs(cands, routeIPv4())
}

// routeIPv4 is this machine's address on the route to the internet, or ""
// without one. Connecting a UDP socket only picks the route: nothing is
// sent.
func routeIPv4() string {
	c, err := net.Dial("udp4", "8.8.8.8:53")
	if err != nil {
		return ""
	}
	defer c.Close()
	if a, ok := c.LocalAddr().(*net.UDPAddr); ok {
		return a.IP.String()
	}
	return ""
}

// rankAddrs orders the addresses: primary (the route to the internet)
// first, then real adapters, then the adapters of virtual machines and
// containers, which other computers cannot reach. The order is otherwise
// kept.
func rankAddrs(cands []ifAddr, primary string) []string {
	rank := func(a ifAddr) int {
		switch {
		case primary != "" && a.ip == primary:
			return 0
		case virtualIf(a.name):
			return 2
		}
		return 1
	}
	sort.SliceStable(cands, func(i, j int) bool { return rank(cands[i]) < rank(cands[j]) })
	out := make([]string, len(cands))
	for i, a := range cands {
		out[i] = a.ip
	}
	return out
}

// virtualIf reports an interface of a virtual machine or container host:
// Hyper-V and WSL (vEthernet), VMware, VirtualBox, Parallels, Docker and
// the macOS bridges of virtual machines.
func virtualIf(name string) bool {
	n := strings.ToLower(name)
	for _, p := range []string{"vethernet", "hyper-v", "wsl", "vmware", "vmnet", "virtualbox", "vboxnet",
		"parallels", "docker", "br-", "veth", "bridge", "virbr"} {
		if strings.Contains(n, p) {
			return true
		}
	}
	return false
}

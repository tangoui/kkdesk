package app

import "testing"

func TestExcludeSelfWindows(t *testing.T) {
	local := []string{"192.168.1.5"}
	cases := []struct {
		remote, env string
		want        bool
	}{
		{"127.0.0.1:50000", "", true},    // self-connect: hide own windows (no mirror)
		{"[::1]:50000", "", true},        // self-connect over IPv6 loopback
		{"192.168.1.5:50000", "", true},  // self-connect via a LAN address
		{"192.168.1.9:50000", "", false}, // another machine: no exclusion filter
		{"127.0.0.1:50000", "0", false},  // forced off
		{"192.168.1.9:50000", "1", true}, // forced on
		{"garbage", "", false},
	}
	for _, c := range cases {
		if got := excludeSelfWindows(c.remote, local, c.env); got != c.want {
			t.Errorf("excludeSelfWindows(%q, env %q) = %v, want %v", c.remote, c.env, got, c.want)
		}
	}
}

package appui

import "testing"

// One field takes a LAN address or a device code; the password is required
// either way.
func TestConnectTarget(t *testing.T) {
	for _, tc := range []struct {
		name       string
		f          Form
		addr, pw   string
		remote, ok bool
	}{
		{"lan", Form{PeerAddr: " 192.168.1.20 ", Password: " k7Pm4x "}, "192.168.1.20", "k7Pm4x", false, true},
		{"device code", Form{PeerAddr: " kd-abcd2345efgh ", Password: "k7Pm4x"}, "KD-ABCD2345EFGH", "k7Pm4x", true, true},
		{"no password", Form{PeerAddr: "192.168.1.20"}, "192.168.1.20", "", false, false},
		{"device code without password", Form{PeerAddr: "KD-ABCD2345EFGH"}, "KD-ABCD2345EFGH", "", true, false},
		{"empty", Form{Password: "k7Pm4x"}, "", "k7Pm4x", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			addr, pw, remote, ok := tc.f.connectTarget()
			if addr != tc.addr || pw != tc.pw || remote != tc.remote || ok != tc.ok {
				t.Fatalf("connectTarget = %q %q %v %v, want %q %q %v %v", addr, pw, remote, ok, tc.addr, tc.pw, tc.remote, tc.ok)
			}
		})
	}
}

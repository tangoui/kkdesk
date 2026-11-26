package appui

import "testing"

// A device code typed or pasted into the LAN address field moves to the
// cross-network field and switches the form to 跨网.
func TestNormalizeViaMovesCode(t *testing.T) {
	f := &Form{Via: viaLAN, PeerAddr: "  KD-ABCD2345EFGH "}
	f.normalizeVia()
	if f.Via != viaRemote || f.PeerCode != "KD-ABCD2345EFGH" || f.PeerAddr != "" {
		t.Fatalf("form %+v", f)
	}
	// An address stays where it is.
	f = &Form{Via: viaLAN, PeerAddr: "192.168.1.20"}
	f.normalizeVia()
	if f.Via != viaLAN || f.PeerAddr != "192.168.1.20" || f.PeerCode != "" {
		t.Fatalf("form %+v", f)
	}
	// An old form without Via (saved before this change) is LAN.
	f = &Form{PeerAddr: "192.168.1.20"}
	f.normalizeVia()
	if f.Via != viaLAN {
		t.Fatalf("default via %q", f.Via)
	}
}

func TestConnectTarget(t *testing.T) {
	for _, tc := range []struct {
		name     string
		f        Form
		addr, pw string
		ok       bool
	}{
		{"lan", Form{Via: viaLAN, PeerAddr: " 192.168.1.20 ", Password: " 123456 "}, "192.168.1.20", "123456", true},
		{"lan empty", Form{Via: viaLAN}, "", "", false},
		{"remote", Form{Via: viaRemote, PeerCode: " kd-abcd2345efgh ", Password: " k7Pm4x "}, "KD-ABCD2345EFGH", "k7Pm4x", true},
		{"remote without password", Form{Via: viaRemote, PeerCode: "KD-ABCD2345EFGH"}, "KD-ABCD2345EFGH", "", true},
		{"remote not a code", Form{Via: viaRemote, PeerCode: "192.168.1.20"}, "", "", false},
		{"remote ignores the LAN address", Form{Via: viaRemote, PeerAddr: "192.168.1.20"}, "", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			addr, pw, ok := tc.f.connectTarget()
			if addr != tc.addr || pw != tc.pw || ok != tc.ok {
				t.Fatalf("connectTarget = %q %q %v, want %q %q %v", addr, pw, ok, tc.addr, tc.pw, tc.ok)
			}
		})
	}
}

package session

import (
	"strings"
	"testing"
)

const testOfferSDP = "v=0\r\no=- 1 2 IN IP4 127.0.0.1\r\ns=-\r\nt=0 0\r\na=fingerprint:sha-256 AA:BB\r\nm=application 9 UDP/DTLS/SCTP webrtc-datachannel\r\nc=IN IP4 0.0.0.0\r\n"

// The controller proves it knows a password without sending it: an HMAC of
// the whole offer (whose DTLS fingerprint is new for every connection)
// rides in the SDP. The host checks it against its passwords and gets the
// SDP back without the line.
func TestRemoteOfferAuth(t *testing.T) {
	signed := signRemoteOffer(testOfferSDP, "k7Pm4x")
	if signed == testOfferSDP || !strings.Contains(signed, remoteAuthAttr) {
		t.Fatalf("not signed:\n%s", signed)
	}
	if strings.Contains(signed, "k7Pm4x") {
		t.Fatal("the password itself is in the SDP")
	}
	clean, ok := VerifyRemoteOffer(signed, "other-password", "k7Pm4x")
	if !ok || clean != testOfferSDP {
		t.Fatalf("verify = %v, clean SDP changed:\n%q", ok, clean)
	}
	if _, ok := VerifyRemoteOffer(signed, "K7Pm4x"); ok {
		t.Fatal("case-insensitive match")
	}
	if _, ok := VerifyRemoteOffer(signed, "", ""); ok {
		t.Fatal("empty passwords accepted")
	}
	// Any change to the offer (another fingerprint) breaks the proof.
	tampered := strings.Replace(signed, "AA:BB", "AA:CC", 1)
	if _, ok := VerifyRemoteOffer(tampered, "k7Pm4x"); ok {
		t.Fatal("tampered offer accepted")
	}
	// No proof: not authenticated, SDP unchanged.
	if clean, ok := VerifyRemoteOffer(testOfferSDP, "k7Pm4x"); ok || clean != testOfferSDP {
		t.Fatalf("unsigned offer: %v %q", ok, clean)
	}
	// No password given: nothing is added.
	if got := signRemoteOffer(testOfferSDP, ""); got != testOfferSDP {
		t.Fatal("signed without a password")
	}
}

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
	clean, auth := VerifyRemoteOffer(signed, "other-password", "k7Pm4x")
	if auth != RemoteAuthOK || clean != testOfferSDP {
		t.Fatalf("verify = %v, clean SDP changed:\n%q", auth, clean)
	}
	if _, auth := VerifyRemoteOffer(signed, "K7Pm4x"); auth != RemoteAuthWrong {
		t.Fatalf("case-insensitive match: %v", auth)
	}
	if _, auth := VerifyRemoteOffer(signed, "", ""); auth != RemoteAuthWrong {
		t.Fatalf("empty passwords: %v", auth)
	}
	// Any change to the offer (another fingerprint) breaks the proof.
	tampered := strings.Replace(signed, "AA:BB", "AA:CC", 1)
	if _, auth := VerifyRemoteOffer(tampered, "k7Pm4x"); auth != RemoteAuthWrong {
		t.Fatalf("tampered offer: %v", auth)
	}
	// No proof (an older controller): SDP unchanged.
	if clean, auth := VerifyRemoteOffer(testOfferSDP, "k7Pm4x"); auth != RemoteAuthNone || clean != testOfferSDP {
		t.Fatalf("unsigned offer: %v %q", auth, clean)
	}
	// No password given: nothing is added.
	if got := signRemoteOffer(testOfferSDP, ""); got != testOfferSDP {
		t.Fatal("signed without a password")
	}
}

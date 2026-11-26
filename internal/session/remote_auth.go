package session

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"strings"
)

// Cross-network connections carry no password exchange of their own: the
// rendezvous server forwards the offer and the answer. A controller that
// knows the host's temporary or fixed password proves it with an HMAC of
// the offer, appended to the SDP as an attribute WebRTC ignores (and that
// rendezvous servers forward verbatim, old ones included). The offer holds
// this connection's DTLS fingerprint, so a captured proof is useless
// without the matching private key; the password itself never leaves the
// controller.
const remoteAuthAttr = "a=x-kkdesk-auth:"

var remoteAuthContext = []byte("kkdesk remote offer v1\x00")

func remoteOfferMAC(sdp, password string) []byte {
	m := hmac.New(sha256.New, []byte(password))
	m.Write(remoteAuthContext)
	m.Write([]byte(sdp))
	return m.Sum(nil)
}

// signRemoteOffer appends the proof of password to the offer SDP; with no
// password it returns the SDP unchanged.
func signRemoteOffer(sdp, password string) string {
	if password == "" {
		return sdp
	}
	if !strings.HasSuffix(sdp, "\r\n") {
		sdp += "\r\n"
	}
	return sdp + remoteAuthAttr + base64.RawStdEncoding.EncodeToString(remoteOfferMAC(sdp, password)) + "\r\n"
}

// VerifyRemoteOffer removes the proof of password from an offer SDP and
// reports whether it matches one of the passwords (empty ones are
// skipped). An offer without a proof is returned unchanged, not verified.
func VerifyRemoteOffer(sdp string, passwords ...string) (clean string, ok bool) {
	i := strings.LastIndex(sdp, remoteAuthAttr)
	if i < 0 || (i > 0 && sdp[i-1] != '\n') {
		return sdp, false
	}
	end := strings.Index(sdp[i:], "\r\n")
	if end < 0 {
		return sdp, false
	}
	value := sdp[i+len(remoteAuthAttr) : i+end]
	clean = sdp[:i] + sdp[i+end+2:]
	mac, err := base64.RawStdEncoding.DecodeString(value)
	if err != nil {
		return clean, false
	}
	for _, pw := range passwords {
		if pw != "" && hmac.Equal(mac, remoteOfferMAC(clean, pw)) {
			ok = true
		}
	}
	return clean, ok
}

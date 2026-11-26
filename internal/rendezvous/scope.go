package rendezvous

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// mobileTokenPrefix marks a phone credential (MobileToken).
const mobileTokenPrefix = "m1."

// MobileToken derives the credential a computer puts in its pairing QR code
// from the service token: it lets the phone reach that computer (deviceID)
// through the service and nothing else, so a photographed QR code or a
// paired phone does not hand out the service token itself. The server
// checks it without storing anything.
func MobileToken(token, deviceID string) string {
	return mobileTokenPrefix + deviceID + "." + mobileTokenMAC([]byte(token), deviceID)
}

func mobileTokenMAC(key []byte, deviceID string) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte("kkdesk mobile scope v1\x00"))
	mac.Write([]byte(deviceID))
	return hex.EncodeToString(mac.Sum(nil))
}

// parseMobileToken returns the device a phone credential is for, if the
// header carries one made from key.
func parseMobileToken(key []byte, header string) (string, bool) {
	rest, ok := strings.CutPrefix(header, "Bearer "+mobileTokenPrefix)
	if !ok {
		return "", false
	}
	device, mac, ok := strings.Cut(rest, ".")
	if !ok || device == "" {
		return "", false
	}
	return device, hmac.Equal([]byte(mac), []byte(mobileTokenMAC(key, device)))
}

// scopeAllows reports whether a phone credential for device may make the
// request opening a connection: only a phone's operations on that computer.
func scopeAllows(device string, msg message) bool {
	switch msg.Type {
	case "mobile_status", "mobile", "mobile_connect":
		return msg.Device == device
	}
	return false
}

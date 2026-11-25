package pairing

import (
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// Message is the signed payload of either the LAN or rendezvous envelope.
type Message struct {
	Type      string `json:"type"`
	Purpose   string `json:"purpose,omitempty"`
	PublicKey string `json:"publicKey,omitempty"`
	Nonce     string `json:"nonce,omitempty"`
	Code      string `json:"code,omitempty"`
	Name      string `json:"name,omitempty"`
	Model     string `json:"model,omitempty"`
	OS        string `json:"os,omitempty"`
	Bypass    bool   `json:"bypass,omitempty"`
	Status    string `json:"status,omitempty"`
	Deadline  int64  `json:"deadline,omitempty"`
	Mode      string `json:"mode,omitempty"`
	SDP       string `json:"sdp,omitempty"`
	Signature string `json:"signature,omitempty"`
}

type Transcript struct {
	Purpose     string
	ClientNonce string
	HostNonce   string
	ClientKey   string
	HostKey     string
}

func (t Transcript) Bytes(m Message) []byte {
	data := []byte("kkdesk/mobile/v1\x00")
	for _, field := range []string{t.Purpose, t.ClientNonce, t.HostNonce, t.ClientKey, t.HostKey,
		m.Type, m.Purpose, m.PublicKey, m.Nonce, m.Code, m.Name, m.Model, m.OS,
		strconv.FormatBool(m.Bypass), m.Status, strconv.FormatInt(m.Deadline, 10), m.Mode, m.SDP} {
		data = binary.BigEndian.AppendUint32(data, uint32(len(field)))
		data = append(data, field...)
	}
	return data
}

func (t Transcript) Sign(private ed25519.PrivateKey, m *Message) {
	m.Signature = Encode(ed25519.Sign(private, t.Bytes(*m)))
}

func (t Transcript) Verify(key string, m Message) bool {
	public, ok := PublicKey(key)
	sig, err := base64.RawURLEncoding.Strict().DecodeString(m.Signature)
	return ok && err == nil && ed25519.Verify(public, t.Bytes(m), sig)
}

func Encode(data []byte) string { return base64.RawURLEncoding.EncodeToString(data) }

func PublicKey(value string) (ed25519.PublicKey, bool) {
	data, err := base64.RawURLEncoding.Strict().DecodeString(value)
	return data, err == nil && len(data) == ed25519.PublicKeySize && Encode(data) == value
}

func ValidNonce(value string) bool {
	_, ok := PublicKey(value)
	return ok
}

func Random() string {
	var data [32]byte
	_, _ = rand.Read(data[:])
	return Encode(data[:])
}

// CodeProof binds the QR secret to this exchange without sending it in
// either the LAN stream or the server's forwarding envelope.
func (t Transcript) CodeProof(code string) string {
	h := hmac.New(sha256.New, []byte(code))
	h.Write(t.Bytes(Message{Type: "pair-code"}))
	return Encode(h.Sum(nil))
}

func VerificationCode(code string, host, phone ed25519.PublicKey) string {
	h := sha256.New()
	h.Write([]byte(code))
	h.Write(host)
	h.Write(phone)
	sum := h.Sum(nil)
	return fmt.Sprintf("%04d", binary.BigEndian.Uint32(sum[:4])%10000)
}

func QRURI(id, code, fingerprint, server, token, serverKey string, lan []string) string {
	q := url.Values{"v": {"1"}, "id": {id}, "c": {code}, "fp": {fingerprint}}
	if server != "" {
		q.Set("s", server)
		q.Set("t", token)
		if serverKey != "" {
			q.Set("pk", serverKey)
		}
	}
	if len(lan) != 0 {
		q.Set("lan", strings.Join(lan, ","))
	}
	return (&url.URL{Scheme: "kkdesk", Host: "pair", RawQuery: q.Encode()}).String()
}

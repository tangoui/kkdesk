package pairing

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/binary"
)

func hostAnswerMessage(offer, answer string) []byte {
	const domain = "kkdesk host answer v2\x00"
	message := make([]byte, 0, len(domain)+8+len(offer)+len(answer))
	message = append(message, domain...)
	message = binary.BigEndian.AppendUint32(message, uint32(len(offer)))
	message = append(message, offer...)
	message = binary.BigEndian.AppendUint32(message, uint32(len(answer)))
	return append(message, answer...)
}

func (s *Store) SignHostAnswer(offer, answer string) string {
	return Encode(ed25519.Sign(s.key, hostAnswerMessage(offer, answer)))
}

func VerifyHostAnswer(key, signature, offer, answer string) bool {
	public, ok := PublicKey(key)
	if !ok {
		return false
	}
	sig, err := base64.RawURLEncoding.Strict().DecodeString(signature)
	return err == nil && len(sig) == ed25519.SignatureSize && Encode(sig) == signature &&
		ed25519.Verify(public, hostAnswerMessage(offer, answer), sig)
}

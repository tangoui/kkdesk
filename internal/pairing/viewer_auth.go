package pairing

import "crypto/ed25519"

// viewerDomain separates a controller's identity signature from every other
// signature of a desktop key.
const viewerDomain = "kkdesk viewer identity v1\x00"

// SignViewer signs transcript (a connection's password transcript) as the
// controller: it lets a host recognize a controller it has let in before.
func (s *Store) SignViewer(transcript []byte) []byte {
	return ed25519.Sign(s.key, append([]byte(viewerDomain), transcript...))
}

// PublicKeyBytes returns the raw Ed25519 public key.
func (s *Store) PublicKeyBytes() []byte {
	public, _ := PublicKey(s.public)
	return public
}

// VerifyViewer checks a controller's SignViewer signature.
func VerifyViewer(public, signature, transcript []byte) bool {
	return len(public) == ed25519.PublicKeySize && len(signature) == ed25519.SignatureSize &&
		ed25519.Verify(public, append([]byte(viewerDomain), transcript...), signature)
}

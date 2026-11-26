package pairing

import (
	"crypto/ed25519"
	"crypto/hkdf"
	"crypto/sha256"
)

// viewerDomain separates a controller's identity signature from every other
// signature.
const viewerDomain = "kkdesk viewer identity v1\x00"

// viewerKeyFor derives the key this computer proves, as a controller, to the
// host with public key hostKey: a host recognizes a controller it let in
// before, but cannot learn the controller's own key (nor its device code)
// or link it across hosts.
func (s *Store) viewerKeyFor(hostKey string) ed25519.PrivateKey {
	seed, err := hkdf.Key(sha256.New, s.key.Seed(), nil, "kkdesk viewer key v1\x00"+hostKey, ed25519.SeedSize)
	if err != nil {
		panic(err) // fixed sizes: cannot happen
	}
	return ed25519.NewKeyFromSeed(seed)
}

// ViewerPublicKey is the encoded key proved to the host hostKey.
func (s *Store) ViewerPublicKey(hostKey string) string {
	return Encode(s.viewerKeyFor(hostKey).Public().(ed25519.PublicKey))
}

// ViewerClaim signs transcript (a connection's password transcript) with the
// key for hostKey and returns that public key and the signature.
func (s *Store) ViewerClaim(hostKey string, transcript []byte) (public, signature []byte) {
	key := s.viewerKeyFor(hostKey)
	return key.Public().(ed25519.PublicKey), ed25519.Sign(key, append([]byte(viewerDomain), transcript...))
}

// VerifyViewer checks a ViewerClaim signature.
func VerifyViewer(public, signature, transcript []byte) bool {
	return len(public) == ed25519.PublicKeySize && len(signature) == ed25519.SignatureSize &&
		ed25519.Verify(public, append([]byte(viewerDomain), transcript...), signature)
}

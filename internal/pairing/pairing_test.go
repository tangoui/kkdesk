package pairing

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"regexp"
	"testing"
	"time"
)

func TestIdentitySurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	first, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID() != second.ID() || first.DeviceCode() != second.DeviceCode() ||
		first.PublicKey() != second.PublicKey() || first.ID() == "" {
		t.Fatal("desktop identity changed across restarts")
	}
}

func TestCodeExpiresAndCanOnlyBeUsedOnce(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	pin := store.Invite(now)
	tx := Transcript{Purpose: "pair", ClientNonce: Random(), HostNonce: Random()}
	if _, err := store.Claim(tx, tx.CodeProof(pin.Code), pin.Expires); err != ErrExpired {
		t.Fatalf("expired code: %v", err)
	}
	fresh := store.Invite(pin.Expires)
	if fresh.Code == pin.Code || fresh.Expires.Sub(pin.Expires) != 3*time.Minute {
		t.Fatal("expired invite was not refreshed")
	}
	wrong := tx
	wrong.HostNonce = Random()
	if _, err := store.Claim(tx, wrong.CodeProof(fresh.Code), pin.Expires); err != ErrExpired {
		t.Fatalf("proof from another challenge: %v", err)
	}
	if _, err := store.Claim(tx, tx.CodeProof(fresh.Code), pin.Expires); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Claim(tx, tx.CodeProof(fresh.Code), pin.Expires); err != ErrExpired {
		t.Fatalf("replayed code: %v", err)
	}
}

func TestPhonePolicyAndImmediateRevocation(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	key, _, _ := ed25519.GenerateKey(nil)
	phone := Phone{PublicKey: Encode(key), Name: "iPhone"}
	if err := store.Save(phone); err != nil {
		t.Fatal(err)
	}
	if saved, ok := store.Phone(phone.PublicKey); !ok || saved.Bypass {
		t.Fatal("pairing must default to confirmation")
	}
	if err := store.Update(phone.PublicKey, func(p *Phone) { p.Bypass = true }); err != nil {
		t.Fatal(err)
	}
	store, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if saved, ok := store.Phone(phone.PublicKey); !ok || !saved.Bypass {
		t.Fatal("bypass permission was not persisted")
	}
	if err := store.Remove(phone.PublicKey); err != nil {
		t.Fatal(err)
	}
	if _, ok := store.Phone(phone.PublicKey); ok {
		t.Fatal("removed phone is still authorized")
	}
}

func TestSignatureBindsChallengeAndSDP(t *testing.T) {
	pub, private, _ := ed25519.GenerateKey(nil)
	exchange := Transcript{Purpose: "connect", ClientNonce: Random(), HostNonce: Random()}
	offer := Message{Type: "offer", Mode: "desktop", SDP: "v=0\r\n"}
	exchange.Sign(private, &offer)
	if !exchange.Verify(Encode(pub), offer) {
		t.Fatal("valid signature rejected")
	}
	tampered := offer
	tampered.SDP += "a=ice-pwd:replacement\r\n"
	if exchange.Verify(Encode(pub), tampered) {
		t.Fatal("modified SDP was accepted")
	}
	exchange.HostNonce = Random()
	if exchange.Verify(Encode(pub), offer) {
		t.Fatal("signature was accepted on another challenge")
	}
}

func TestManualInviteUsesOneSharedClaimAndKeepsBothCodesOffWire(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	invite := store.Invite(now)
	if !regexp.MustCompile("^[A-Z2-7]{16}$").MatchString(invite.Manual) {
		t.Fatal("manual code must encode 80 bits in 16 base32 characters")
	}
	tx := Transcript{Purpose: "pair_manual", ClientNonce: Random(), HostNonce: Random()}
	if _, err := store.Claim(tx, tx.CodeProof("AAAAAAAAAAAAAAAA"), now); err != ErrExpired {
		t.Fatalf("wrong manual code: %v", err)
	}
	wire, err := json.Marshal(Message{Type: "request", Code: tx.CodeProof(invite.Manual)})
	if err != nil || bytes.Contains(wire, []byte(invite.Manual)) || bytes.Contains(wire, []byte(invite.Code)) {
		t.Fatal("pairing secret exposed on the wire")
	}
	if proof := store.ManualHostProof(tx, now); proof != tx.ManualHostProof(invite.Manual) || proof == tx.CodeProof(invite.Manual) {
		t.Fatal("host code proof must bind the exchange and be distinct from the phone proof")
	}
	if _, err := store.Claim(tx, tx.CodeProof(invite.Manual), now); err != nil {
		t.Fatalf("manual claim: %v", err)
	}
	tx.Purpose = "pair"
	if _, err := store.Claim(tx, tx.CodeProof(invite.Code), now); err != ErrExpired {
		t.Fatalf("QR was reusable after manual claim: %v", err)
	}
}

func TestP256SignatureBindsModeSDPKeyAndChallenge(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tx := Transcript{Purpose: "connect", ClientNonce: Random(), HostNonce: Random()}
	msg := Message{Type: "offer", Mode: "files", SDP: "v=0\r\n",
		BiometricKey: Encode(elliptic.Marshal(elliptic.P256(), key.X, key.Y))}
	hash := sha256.Sum256(tx.Bytes(msg))
	signature, err := ecdsa.SignASN1(rand.Reader, key, hash[:])
	if err != nil {
		t.Fatal(err)
	}
	msg.BiometricSignature = Encode(signature)
	if !tx.VerifyBiometric(msg.BiometricKey, msg) {
		t.Fatal("valid biometric signature was rejected")
	}
	for _, change := range []func(*Message){
		func(m *Message) { m.Mode = "desktop" },
		func(m *Message) { m.SDP += "a=ice-pwd:tampered\r\n" },
		func(m *Message) { m.BiometricKey = "" },
		func(m *Message) { m.BiometricSignature = "" },
	} {
		altered := msg
		change(&altered)
		if tx.VerifyBiometric(msg.BiometricKey, altered) {
			t.Fatal("tampered or missing biometric proof was accepted")
		}
	}
	tx.HostNonce = Random()
	if tx.VerifyBiometric(msg.BiometricKey, msg) {
		t.Fatal("biometric proof reused on a fresh challenge")
	}
}

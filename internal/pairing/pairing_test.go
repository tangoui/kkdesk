package pairing

import (
	"crypto/ed25519"
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

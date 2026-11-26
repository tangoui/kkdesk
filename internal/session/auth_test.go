package session

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tangoui/kkdesk/internal/pairing"
)

type authVector struct {
	Seed, HostKey, HostSig, Offer, Answer, Password, Proof, Code string
}

func readAuthVector(t *testing.T) (authVector, *pairing.Store) {
	t.Helper()
	data, err := os.ReadFile("testdata/auth-v2.json")
	if err != nil {
		t.Fatal(err)
	}
	var v authVector
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatal(err)
	}
	seed, ok := pairing.PublicKey(v.Seed)
	if !ok {
		t.Fatal("invalid fixture seed")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "desktop-identity.key"), seed, 0600); err != nil {
		t.Fatal(err)
	}
	identity, err := pairing.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	return v, identity
}

func TestAuthV2Vector(t *testing.T) {
	v, identity := readAuthVector(t)
	if identity.PublicKey() != v.HostKey || identity.DeviceCode() != v.Code {
		t.Fatal("fixture identity mismatch")
	}
	if got := identity.SignHostAnswer(v.Offer, v.Answer); got != v.HostSig {
		t.Fatalf("signature = %s, want %s", got, v.HostSig)
	}
	for _, tc := range []struct {
		offer, answer, key string
		valid              bool
	}{
		{v.Offer, v.Answer, v.HostKey, true},
		{v.Offer + " ", v.Answer, v.HostKey, false},
		{v.Offer, v.Answer + " ", v.HostKey, false},
		{v.Offer, v.Answer, pairing.Encode(make([]byte, 32)), false},
	} {
		if got := pairing.VerifyHostAnswer(tc.key, v.HostSig, tc.offer, tc.answer); got != tc.valid {
			t.Errorf("verify %+v = %v", tc, got)
		}
	}
	transcript := NewPasswordTranscript(v.Offer, v.Answer)
	if got := pairing.Encode(transcript.Proof(v.Password)); got != v.Proof {
		t.Fatalf("proof = %s, want %s", got, v.Proof)
	}
	for _, changed := range []struct{ offer, answer, password string }{
		{v.Offer + " ", v.Answer, v.Password},
		{v.Offer, v.Answer + " ", v.Password},
		{v.Offer, v.Answer, "wrong"},
	} {
		if got := pairing.Encode(NewPasswordTranscript(changed.offer, changed.answer).Proof(changed.password)); got == v.Proof {
			t.Fatal("changed transcript or password produced the original proof")
		}
	}
}

func TestSignedRemoteAnswer(t *testing.T) {
	v, identity := readAuthVector(t)
	signed := signRemoteAnswer(identity, v.Offer, v.Answer)
	clean, err := verifyRemoteAnswer(v.Offer, signed, " kd-"+v.Code[3:]+" ")
	if err != nil || clean != v.Answer {
		t.Fatalf("verify = %q, %v", clean, err)
	}
	if _, err := verifyRemoteAnswer(v.Offer, signed, "KD-AAAAAAAAAAAA"); !errors.Is(err, ErrWrongHostCode) {
		t.Fatalf("wrong device code: %v", err)
	}
	for _, sdp := range []string{
		v.Answer,
		v.Answer + "x" + signed[len(v.Answer):],
		signed + "x" + remoteHostSigAttr + v.HostSig + "\r\n",
		signed + "a=anything\r\n",
	} {
		if _, err := verifyRemoteAnswer(v.Offer, sdp, v.Code); !errors.Is(err, ErrHostSignature) {
			t.Fatalf("invalid answer accepted or misclassified: %q: %v", sdp, err)
		}
	}
}

func TestPasswordLockout(t *testing.T) {
	var gate PasswordGate
	now := time.Unix(1000, 0)
	tx := NewPasswordTranscript("offer", "answer")
	wrong := tx.Proof("wrong")
	check := func(proof []byte, want string, wait time.Duration) {
		t.Helper()
		if got, remaining := gate.Check(now, tx, proof, "temporary", "fixed"); got != want || remaining != wait {
			t.Fatalf("check = %q, %s; want %q, %s", got, remaining, want, wait)
		}
	}
	for range 4 {
		check(wrong, "", 0)
		if gate.Remaining(now) != 0 {
			t.Fatal("locked too soon")
		}
	}
	for _, lock := range []time.Duration{30, 60, 120, 240, 480, 900, 900} {
		lock *= time.Second
		check(wrong, "", 0)
		if remaining := gate.Remaining(now); remaining != lock {
			t.Fatalf("locked for %s, want %s", remaining, lock)
		}
		now = now.Add(time.Second)
		check(wrong, "", lock-time.Second)
		check(tx.Proof("fixed"), "", lock-time.Second)
		now = now.Add(lock - time.Second)
	}
	check(tx.Proof("fixed"), "fixed", 0)
	for range 4 {
		check(wrong, "", 0)
	}
	check(tx.Proof("temporary"), "temporary", 0)
	for range 4 {
		check(wrong, "", 0)
	}
	if gate.Remaining(now) != 0 {
		t.Fatal("success did not clear failure count")
	}
}

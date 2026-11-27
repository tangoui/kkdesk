package session

import (
	"encoding/json"
	"errors"
	"fmt"
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
		if got, remaining := gate.Check(now, "lan:192.0.2.1", tx, proof, "temporary", "fixed"); got != want || remaining != wait {
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

func TestPasswordLockoutIsPerSource(t *testing.T) {
	var gate PasswordGate
	now := time.Unix(1000, 0)
	tx := NewPasswordTranscript("offer", "answer")
	for range 5 {
		gate.Check(now, "remote", tx, tx.Proof("wrong"), "temporary", "")
	}
	if _, remaining := gate.Check(now, "remote", tx, tx.Proof("temporary"), "temporary", ""); remaining == 0 {
		t.Fatal("five failures did not pause their source")
	}
	if got, remaining := gate.Check(now, "lan:192.0.2.7", tx, tx.Proof("temporary"), "temporary", ""); got != "temporary" || remaining != 0 {
		t.Fatalf("another source = %q, %s; want accepted while remote is paused", got, remaining)
	}
}

func TestPasswordFailuresAreForgotten(t *testing.T) {
	var gate PasswordGate
	now := time.Unix(1000, 0)
	tx := NewPasswordTranscript("offer", "answer")
	for range 4 {
		gate.Check(now, "remote", tx, tx.Proof("wrong"), "temporary", "")
	}
	now = now.Add(16 * time.Minute)
	gate.Check(now, "remote", tx, tx.Proof("wrong"), "temporary", "")
	if remaining := gate.Remaining(now); remaining != 0 {
		t.Fatalf("a failure 16 minutes after the last ones paused for %s; old failures were not forgotten", remaining)
	}
}

func TestPasswordGateReset(t *testing.T) {
	var gate PasswordGate
	now := time.Unix(1000, 0)
	tx := NewPasswordTranscript("offer", "answer")
	for range 10 {
		gate.Check(now, "remote", tx, tx.Proof("wrong"), "temporary", "")
	}
	gate.Reset()
	if got, remaining := gate.Check(now, "remote", tx, tx.Proof("temporary"), "temporary", ""); got != "temporary" || remaining != 0 {
		t.Fatalf("after Reset = %q, %s; want accepted", got, remaining)
	}
}

func TestPasswordGateBoundsSources(t *testing.T) {
	var gate PasswordGate
	now := time.Unix(1000, 0)
	tx := NewPasswordTranscript("offer", "answer")
	for i := range 5000 {
		gate.Check(now, fmt.Sprintf("lan:10.0.%d.%d", i/256, i%256), tx, tx.Proof("wrong"), "temporary", "")
	}
	if n := gate.sourceCount(); n > maxGateSources {
		t.Fatalf("gate tracks %d sources, want at most %d", n, maxGateSources)
	}
}

// Flooding the gate with new sources cannot evict (and so unpause) a source
// that is paused or failed recently.
func TestPasswordGateFloodDoesNotUnpause(t *testing.T) {
	var gate PasswordGate
	now := time.Unix(1000, 0)
	tx := NewPasswordTranscript("offer", "answer")
	for range 5 {
		gate.Check(now, "lan:192.0.2.1", tx, tx.Proof("wrong"), "temporary", "")
	}
	for i := range 3 * maxGateSources {
		gate.Check(now, fmt.Sprintf("lan:10.1.%d.%d", i/256, i%256), tx, tx.Proof("wrong"), "temporary", "")
	}
	if _, remaining := gate.Check(now, "lan:192.0.2.1", tx, tx.Proof("temporary"), "temporary", ""); remaining == 0 {
		t.Fatal("a flood of other sources unpaused a paused source")
	}
}

// Failures fade (halve per quiet period) rather than vanish at once.
func TestPasswordFailuresFade(t *testing.T) {
	var gate PasswordGate
	now := time.Unix(1000, 0)
	tx := NewPasswordTranscript("offer", "answer")
	for range 4 {
		gate.Check(now, "remote", tx, tx.Proof("wrong"), "temporary", "")
	}
	now = now.Add(16 * time.Minute) // 4 → 2
	for range 3 {
		gate.Check(now, "remote", tx, tx.Proof("wrong"), "temporary", "")
	}
	if gate.Remaining(now) == 0 {
		t.Fatal("4 old + 3 new failures did not pause: old failures were forgotten entirely")
	}
}

// Past a total failure budget per hour, any source that has failed is
// paused, while sources that never failed still get in.
func TestPasswordGateGlobalBudget(t *testing.T) {
	var gate PasswordGate
	now := time.Unix(1000, 0)
	tx := NewPasswordTranscript("offer", "answer")
	gate.Check(now, "lan:192.0.2.9", tx, tx.Proof("wrong"), "temporary", "")
	for i := range globalFailBudget {
		gate.Check(now, fmt.Sprintf("lan:10.2.0.%d", i), tx, tx.Proof("wrong"), "temporary", "")
	}
	if _, remaining := gate.Check(now, "lan:192.0.2.9", tx, tx.Proof("temporary"), "temporary", ""); remaining == 0 {
		t.Fatal("a source that failed before was let through past the hourly budget")
	}
	if got, remaining := gate.Check(now, "lan:192.0.2.50", tx, tx.Proof("temporary"), "temporary", ""); got != "temporary" || remaining != 0 {
		t.Fatalf("a source that never failed = %q, %s; want accepted", got, remaining)
	}
}

func TestSourceKeyGroupsIPv6Prefixes(t *testing.T) {
	for _, tc := range []struct{ ip, want string }{
		{"192.0.2.7", "lan:192.0.2.7"},
		{"2001:db8:1:2:aaaa::1", "lan:2001:db8:1:2::/64"},
		{"2001:db8:1:2:bbbb::9", "lan:2001:db8:1:2::/64"},
		{"::1", "lan:::/64"},
		{"not-an-ip", "lan:not-an-ip"},
	} {
		if got := SourceKey(tc.ip); got != tc.want {
			t.Errorf("SourceKey(%q) = %q, want %q", tc.ip, got, tc.want)
		}
	}
}

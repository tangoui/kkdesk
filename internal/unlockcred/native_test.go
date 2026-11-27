//go:build (darwin && cgo) || windows

package unlockcred

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"testing"
)

func TestCredentialRoundTrip(t *testing.T) {
	var suffix [16]byte
	rand.Read(suffix[:])
	store := nativeStore("kkdesk.unlock.test." + hex.EncodeToString(suffix[:]))
	account := "test-account"
	t.Cleanup(func() {
		if err := store.Delete(account); err != nil && !errors.Is(err, ErrNotFound) && !errors.Is(err, errStoreUnavailable) {
			t.Errorf("delete test credential: %v", err)
		}
	})
	secret := []byte("集成测试 PIN 123")
	if err := store.Write(account, secret); errors.Is(err, errStoreUnavailable) {
		t.Skipf("system credential store is unavailable in this login session: %v", err)
	} else if err != nil {
		t.Fatal(err)
	}
	saved, err := store.Read(account)
	if err != nil || !bytes.Equal(saved, secret) {
		t.Fatal("credential did not round-trip")
	}
	clear(saved)
	replacement := []byte("replacement PIN")
	if err := store.Write(account, replacement); err != nil {
		t.Fatal(err)
	}
	saved, err = store.Read(account)
	if err != nil || !bytes.Equal(saved, replacement) {
		t.Fatal("replacement credential did not round-trip")
	}
	clear(saved)
	if err := store.Delete(account); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Read(account); !errors.Is(err, ErrNotFound) {
		t.Fatalf("read after delete: %v", err)
	}
}

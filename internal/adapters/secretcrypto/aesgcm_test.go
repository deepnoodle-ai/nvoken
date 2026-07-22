package secretcrypto

import (
	"bytes"
	"testing"
)

func TestKeyringEncryptsWithAssociatedDataAndSupportsOldKeys(t *testing.T) {
	oldKey := bytes.Repeat([]byte{1}, KeyBytes)
	newKey := bytes.Repeat([]byte{2}, KeyBytes)
	oldRing, err := NewKeyring("old", map[string][]byte{"old": oldKey})
	if err != nil {
		t.Fatalf("old keyring: %v", err)
	}
	encrypted, err := oldRing.Encrypt([]byte("provider-secret"), []byte("binding-a"))
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if bytes.Contains(encrypted.Ciphertext, []byte("provider-secret")) {
		t.Fatal("ciphertext contains plaintext")
	}
	rotated, err := NewKeyring("new", map[string][]byte{"old": oldKey, "new": newKey})
	if err != nil {
		t.Fatalf("rotated keyring: %v", err)
	}
	plaintext, err := rotated.Decrypt(encrypted, []byte("binding-a"))
	if err != nil || string(plaintext) != "provider-secret" {
		t.Fatalf("decrypt = %q, %v", plaintext, err)
	}
	if _, err := rotated.Decrypt(encrypted, []byte("binding-b")); err == nil {
		t.Fatal("different associated data decrypted")
	}
}

func TestKeyringFailsSafe(t *testing.T) {
	if _, err := NewKeyring("missing", map[string][]byte{"other": bytes.Repeat([]byte{1}, KeyBytes)}); err == nil {
		t.Fatal("missing active key accepted")
	}
	if _, err := NewKeyring("short", map[string][]byte{"short": []byte("short")}); err == nil {
		t.Fatal("short key accepted")
	}
}

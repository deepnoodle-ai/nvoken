// Package secretcrypto provides application-layer credential encryption.
package secretcrypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"fmt"
	"io"

	"github.com/deepnoodle-ai/nvoken/internal/domain"
)

const KeyBytes = 32

type Keyring struct {
	active string
	keys   map[string][]byte
	random io.Reader
}

func NewKeyring(active string, keys map[string][]byte) (*Keyring, error) {
	if active == "" {
		return nil, fmt.Errorf("active credential encryption key ID is required")
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("credential encryption keys are required")
	}
	owned := make(map[string][]byte, len(keys))
	for id, key := range keys {
		if id == "" {
			return nil, fmt.Errorf("credential encryption key ID is required")
		}
		if len(key) != KeyBytes {
			return nil, fmt.Errorf("credential encryption key %q must contain %d bytes", id, KeyBytes)
		}
		owned[id] = append([]byte(nil), key...)
	}
	if _, ok := owned[active]; !ok {
		return nil, fmt.Errorf("active credential encryption key %q is not present", active)
	}
	return &Keyring{active: active, keys: owned, random: rand.Reader}, nil
}

func (k *Keyring) Encrypt(plaintext, associatedData []byte) (domain.EncryptedCredential, error) {
	if k == nil {
		return domain.EncryptedCredential{}, fmt.Errorf("credential encryption is not configured")
	}
	aead, err := k.aead(k.active)
	if err != nil {
		return domain.EncryptedCredential{}, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(k.random, nonce); err != nil {
		return domain.EncryptedCredential{}, fmt.Errorf("generate credential encryption nonce: %w", err)
	}
	return domain.EncryptedCredential{
		KeyID:      k.active,
		Nonce:      nonce,
		Ciphertext: aead.Seal(nil, nonce, plaintext, associatedData),
	}, nil
}

func (k *Keyring) Decrypt(encrypted domain.EncryptedCredential, associatedData []byte) ([]byte, error) {
	if k == nil {
		return nil, fmt.Errorf("credential encryption is not configured")
	}
	aead, err := k.aead(encrypted.KeyID)
	if err != nil {
		return nil, err
	}
	if len(encrypted.Nonce) != aead.NonceSize() || len(encrypted.Ciphertext) <= aead.Overhead() {
		return nil, fmt.Errorf("encrypted credential envelope is invalid")
	}
	plaintext, err := aead.Open(nil, encrypted.Nonce, encrypted.Ciphertext, associatedData)
	if err != nil {
		return nil, fmt.Errorf("decrypt credential: authentication failed")
	}
	return plaintext, nil
}

func (k *Keyring) aead(id string) (cipher.AEAD, error) {
	key, ok := k.keys[id]
	if !ok {
		return nil, fmt.Errorf("credential encryption key %q is unavailable", id)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("configure credential cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("configure credential AEAD: %w", err)
	}
	return aead, nil
}

// Package secret encrypts values that must be readable again later
// (router passwords, PPPoE/hotspot secrets) with AES-256-GCM.
package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
)

// Seal encrypts plaintext with a 32-byte key. The random nonce is prepended.
func Seal(key, plaintext []byte) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	rand.Read(nonce)
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

// Open decrypts a value produced by Seal.
func Open(key, sealed []byte) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	if len(sealed) < gcm.NonceSize() {
		return nil, errors.New("secret: ciphertext too short")
	}
	n := gcm.NonceSize()
	return gcm.Open(nil, sealed[:n], sealed[n:], nil)
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// LoadKey returns the 32-byte key from hexKey (NUXBILL_SECRET_KEY). When hexKey
// is empty it reads the key from path, creating it with a random key first.
func LoadKey(hexKey, path string) (key []byte, created bool, err error) {
	if hexKey == "" {
		b, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			key = make([]byte, 32)
			rand.Read(key)
			return key, true, os.WriteFile(path, []byte(hex.EncodeToString(key)+"\n"), 0o600)
		}
		if err != nil {
			return nil, false, err
		}
		hexKey = string(b)
	}
	key, err = hex.DecodeString(strings.TrimSpace(hexKey))
	if err != nil || len(key) != 32 {
		return nil, false, fmt.Errorf("secret: key must be 64 hex characters (32 bytes)")
	}
	return key, false, nil
}

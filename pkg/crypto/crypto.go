// Package crypto provides AES-256-GCM encryption for secrets stored at rest,
// such as connection configurations. Keys are 32-byte values supplied as hex
// strings from the security.encryption_key config field.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
)

// ErrDecrypt marks a value that could not be opened with the configured key,
// which in practice means security.encryption_key changed after the value was
// stored. Callers use it to tell recoverable config drift, where the operator
// re-enters the secret, apart from a genuine fault.
var ErrDecrypt = errors.New("failed to decrypt")

// KeyFromHex decodes a 64-character hex string into a 32-byte AES-256 key.
func KeyFromHex(s string) ([]byte, error) {
	if s == "" {
		return nil, fmt.Errorf("encryption key is not configured")
	}
	key, err := hex.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("encryption key is not valid hex: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("encryption key must be 32 bytes, got %d", len(key))
	}
	return key, nil
}

// Encrypt seals plaintext with AES-256-GCM and returns base64(nonce||ciphertext).
func Encrypt(key, plaintext []byte) (string, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("failed to generate nonce: %w", err)
	}
	sealed := gcm.Seal(nonce, nonce, plaintext, nil)
	return base64.StdEncoding.EncodeToString(sealed), nil
}

// Decrypt opens a base64(nonce||ciphertext) value produced by Encrypt.
func Decrypt(key []byte, encoded string) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("ciphertext is not valid base64: %w", err)
	}
	if len(raw) < gcm.NonceSize() {
		return nil, fmt.Errorf("ciphertext is too short")
	}
	nonce, ciphertext := raw[:gcm.NonceSize()], raw[gcm.NonceSize():]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrDecrypt, err)
	}
	return plaintext, nil
}

// newGCM builds an AES-GCM AEAD from a 32-byte key.
func newGCM(key []byte) (cipher.AEAD, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("encryption key must be 32 bytes, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

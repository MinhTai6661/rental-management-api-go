// Package secretbox encrypts small secrets that must be read back later (the link of a bill that the landlord copies
// again) with AES-256-GCM. A box is made from a 32-byte key; Seal output is base64 of a fresh random nonce followed by
// the ciphertext and its authentication tag, so the same text seals to different strings, and a changed or truncated
// value (or another key) fails to open instead of giving back garbage.
package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
)

// KeySize is the length of the key in bytes (AES-256).
const KeySize = 32

// ErrOpen is returned when a value cannot be opened: it is not a sealed value, it was changed, or the key is wrong.
// It never says which, and never carries the value.
var ErrOpen = errors.New("secretbox: cannot open the value")

// Box seals and opens values with one key.
type Box struct{ aead cipher.AEAD }

// New makes a box from a key of exactly KeySize bytes.
func New(key []byte) (*Box, error) {
	if len(key) != KeySize {
		return nil, fmt.Errorf("secretbox: the key must be %d bytes, got %d", KeySize, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("secretbox: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("secretbox: %w", err)
	}
	return &Box{aead: aead}, nil
}

// ParseKey reads a key written as standard base64 (e.g. the output of `openssl rand -base64 32`) and checks its length.
// The error names the setting's problem, never the key.
func ParseKey(encoded string) ([]byte, error) {
	key, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, errors.New("is not valid base64")
	}
	if len(key) != KeySize {
		return nil, fmt.Errorf("must decode to %d bytes, got %d", KeySize, len(key))
	}
	return key, nil
}

// Seal encrypts plain.
func (b *Box) Seal(plain string) (string, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("secretbox: make a nonce: %w", err)
	}
	return base64.StdEncoding.EncodeToString(b.aead.Seal(nonce, nonce, []byte(plain), nil)), nil
}

// Open decrypts a value made by Seal with the same key.
func (b *Box) Open(sealed string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(sealed)
	if err != nil || len(raw) < b.aead.NonceSize()+b.aead.Overhead() {
		return "", ErrOpen
	}
	nonce, ciphertext := raw[:b.aead.NonceSize()], raw[b.aead.NonceSize():]
	plain, err := b.aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", ErrOpen
	}
	return string(plain), nil
}

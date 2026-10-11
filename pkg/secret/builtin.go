package secret

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
)

// ErrInvalidKey reports a key-encryption key that is not 32 random bytes.
var ErrInvalidKey = errors.New("invalid key-encryption key")

const (
	// builtinKeyBytes is the size of the built-in key-encryption key.
	builtinKeyBytes = 32
	// builtinIDPrefix marks wrapper IDs of the built-in key.
	builtinIDPrefix = "builtin-"
	// builtinIDHexChars keeps the first 16 hexadecimal characters of the
	// HMAC-SHA256 over builtinIDLabel, enough to tell keys apart.
	builtinIDHexChars = 16
	// builtinIDLabel is the fixed text the wrapper ID derives from (ADR 23).
	builtinIDLabel = "opendustpan wrapper id v1"
)

// BuiltinWrapper is the built-in Wrapper adapter: it seals data keys with
// AES-256-GCM under a 32-byte key-encryption key that the operator provides
// (ADR 23). It never reveals the key; ID names the key through a one-way
// function of it.
type BuiltinWrapper struct {
	aead cipher.AEAD
	id   string
}

var _ Wrapper = (*BuiltinWrapper)(nil)

// NewBuiltinWrapper returns a Wrapper that seals data keys under key, which
// must hold 32 random bytes.
func NewBuiltinWrapper(key []byte) (*BuiltinWrapper, error) {
	if len(key) != builtinKeyBytes {
		return nil, fmt.Errorf("%w: need %d bytes, got %d", ErrInvalidKey, builtinKeyBytes, len(key))
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidKey, err)
	}

	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidKey, err)
	}

	id, err := builtinID(key)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidKey, err)
	}

	return &BuiltinWrapper{aead: aead, id: id}, nil
}

// ID implements Wrapper.
func (w *BuiltinWrapper) ID() string {
	return w.id
}

// Wrap implements Wrapper. It seals the data key with a random nonce and
// binds it to aad, and returns the nonce followed by the sealed key.
func (w *BuiltinWrapper) Wrap(_ context.Context, key, aad []byte) ([]byte, error) {
	nonce := make([]byte, w.aead.NonceSize())

	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}

	return w.aead.Seal(nonce, nonce, key, aad), nil
}

// Unwrap implements Wrapper. It returns ErrDecrypt when the ciphertext or
// the associated data differs from what Wrap produced and was given.
func (w *BuiltinWrapper) Unwrap(_ context.Context, ciphertext, aad []byte) ([]byte, error) {
	size := w.aead.NonceSize()
	if len(ciphertext) < size {
		return nil, ErrDecrypt
	}

	key, err := w.aead.Open(nil, ciphertext[:size], ciphertext[size:], aad)
	if err != nil {
		return nil, ErrDecrypt
	}

	return key, nil
}

// builtinID names key as builtinIDPrefix followed by the first
// builtinIDHexChars hexadecimal characters of HMAC-SHA256, keyed with the
// key, over builtinIDLabel. A one-way function makes it, so it does not
// contain the key.
func builtinID(key []byte) (string, error) {
	mac := hmac.New(sha256.New, key)

	// hash.Hash.Write never returns an error; the check satisfies errcheck.
	if _, err := mac.Write([]byte(builtinIDLabel)); err != nil {
		return "", fmt.Errorf("fingerprint key: %w", err)
	}

	return builtinIDPrefix + hex.EncodeToString(mac.Sum(nil))[:builtinIDHexChars], nil
}

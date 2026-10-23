// Package pagetoken encodes and decodes the opaque page tokens of the HTTP
// adapters. A token is the JSON of the wire struct of an adapter (the cursor
// and the fingerprint of the query), in base64url with no padding. Each
// adapter keeps its own wire struct, so its token shape does not change.
package pagetoken

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// MaxLen is the length of the longest token, in bytes, that Decode reads. A
// token of the longest cursor of the adapters is below 1 KiB.
const MaxLen = 4096

// ErrInvalid reports a page token that is too long, that does not decode, or
// that does not hold one JSON object of the wire shape. Each adapter maps it
// to its own refusal of the token.
var ErrInvalid = errors.New("page token is not valid")

// Fingerprint returns the short digest of the query values in input: the
// first 8 bytes of its SHA-256, in hex.
func Fingerprint(input string) string {
	sum := sha256.Sum256([]byte(input))

	return hex.EncodeToString(sum[:8])
}

// SameFingerprint reports whether got equals want. It compares in constant
// time, so the time of a refusal does not tell how much of a fingerprint
// matched.
func SameFingerprint(got, want string) bool {
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

// Encode returns the token of wire.
func Encode(wire any) (string, error) {
	raw, err := json.Marshal(wire)
	if err != nil {
		return "", fmt.Errorf("encode page token: %w", err)
	}

	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// Decode reads token into wire, a pointer to the wire struct of the adapter.
// It reads at most MaxLen bytes, and it refuses a token that is not base64url,
// a token that is not one JSON object, and a member of another type. On an
// error the caller must not use wire. The caller checks the fingerprint and
// the cursor.
func Decode(token string, wire any) error {
	if token == "" || len(token) > MaxLen {
		return ErrInvalid
	}

	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) == 0 || raw[0] != '{' {
		return ErrInvalid
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	if err = dec.Decode(wire); err != nil {
		return ErrInvalid
	}

	if _, err = dec.Token(); !errors.Is(err, io.EOF) {
		return ErrInvalid
	}

	return nil
}

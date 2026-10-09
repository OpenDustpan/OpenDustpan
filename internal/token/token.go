// Package token creates and checks bearer capabilities: the lease token that
// authorizes writes to a running update, and the URL that lets a client upload
// one state version. A token is shown to its client once; servers keep only
// its digest, so reading the database does not yield usable tokens.
package token

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
)

// ErrInvalid reports a string that is not a well-formed token.
var ErrInvalid = errors.New("invalid token")

const (
	entropyBytes = 32
	encodedLen   = 43 // base64url without padding of entropyBytes
	redacted     = "[token]"
)

// Token is a secret bearer value. Its String method redacts it, so it cannot
// leak through logs or error messages by accident; use Reveal to send it.
type Token struct {
	value string
}

// Digest is what a server stores in place of a token.
type Digest [sha256.Size]byte

// New returns a token with 256 bits of entropy.
func New() (Token, error) {
	b := make([]byte, entropyBytes)
	if _, err := rand.Read(b); err != nil {
		return Token{}, fmt.Errorf("generate token: %w", err)
	}

	return Token{value: base64.RawURLEncoding.EncodeToString(b)}, nil
}

// Parse accepts a token received from a client.
func Parse(s string) (Token, error) {
	if len(s) != encodedLen {
		return Token{}, ErrInvalid
	}

	if _, err := base64.RawURLEncoding.DecodeString(s); err != nil {
		return Token{}, ErrInvalid
	}

	return Token{value: s}, nil
}

// Reveal returns the token's value, for handing it to its client.
func (t Token) Reveal() string {
	return t.value
}

// String redacts the token.
func (Token) String() string {
	return redacted
}

// GoString redacts the token in %#v output too.
func (Token) GoString() string {
	return redacted
}

// Digest returns the value to store for t.
func (t Token) Digest() Digest {
	return sha256.Sum256([]byte(t.value))
}

// IsZero reports whether d is the zero digest, which no token produces.
func (d Digest) IsZero() bool {
	return d == Digest{}
}

// Matches reports, in constant time, whether t is the token d was made from.
// The zero digest matches nothing.
func (d Digest) Matches(t Token) bool {
	if d.IsZero() || t.value == "" {
		return false
	}

	got := t.Digest()

	return subtle.ConstantTimeCompare(d[:], got[:]) == 1
}

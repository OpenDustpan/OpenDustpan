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
	"reflect"
	"strings"
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
// It reads at most MaxLen bytes, and it refuses a token that is not the
// canonical base64url form, a token that is not one JSON object, a member
// that the wire struct does not have, and a member of another type. On an
// error the caller must not use wire. The caller checks the fingerprint and
// the cursor.
func Decode(token string, wire any) error {
	if token == "" || len(token) > MaxLen {
		return ErrInvalid
	}

	// Defense in depth: Go's base64url decoder accepts CR and LF inside the
	// text and ignores unused bits in the final character; this check stays so
	// that a token that decodes to the same bytes but is not the encoder form
	// cannot reach the cursor. Encode writes the canonical form.
	if !isCanonicalBase64URL(token) {
		return ErrInvalid
	}

	raw, err := base64.RawURLEncoding.Strict().DecodeString(token)
	if err != nil || len(raw) == 0 || raw[0] != '{' {
		return ErrInvalid
	}

	// Defense in depth: Strict() refuses unused bits; this check stays so that
	// only the exact encoder form is accepted even if a decoder is swapped.
	if base64.RawURLEncoding.EncodeToString(raw) != token {
		return ErrInvalid
	}

	// Defense in depth: the strict decode below checks the shape; this check
	// stays so that a repeated member name or a name that differs only in case
	// from a wire name cannot make two decoders read different values.
	err = checkExactMemberNames(raw, wire)
	if err != nil {
		return err
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()

	if err = dec.Decode(wire); err != nil {
		return ErrInvalid
	}

	if _, err = dec.Token(); !errors.Is(err, io.EOF) {
		return ErrInvalid
	}

	return nil
}

// checkExactMemberNames refuses raw when it is not one JSON object whose
// member names are exactly the JSON names of the wire struct, each once.
// Defense in depth: DisallowUnknownFields rejects unknown names and the wire
// decode reads the typed values; this check stays so that a repeated name or
// a case-variant name cannot be read by one decoder and ignored by another.
func checkExactMemberNames(raw []byte, wire any) error {
	names, err := wireNames(wire)
	if err != nil {
		return ErrInvalid
	}

	dec := json.NewDecoder(bytes.NewReader(raw))

	err = readDelim(dec, '{')
	if err != nil {
		return err
	}

	err = checkMembers(dec, names)
	if err != nil {
		return err
	}

	err = readDelim(dec, '}')
	if err != nil {
		return err
	}

	if _, err = dec.Token(); !errors.Is(err, io.EOF) {
		return ErrInvalid
	}

	return nil
}

// readDelim reads the next JSON token and refuses it when it is not want.
func readDelim(dec *json.Decoder, want json.Delim) error {
	tok, err := dec.Token()
	if err != nil {
		return ErrInvalid
	}

	got, ok := tok.(json.Delim)
	if !ok || got != want {
		return ErrInvalid
	}

	return nil
}

// checkMembers checks that each member name of the object is a wire name and
// is present once, and reads past its value.
func checkMembers(dec *json.Decoder, names map[string]struct{}) error {
	seen := make(map[string]struct{}, len(names))

	for dec.More() {
		key, err := memberName(dec)
		if err != nil {
			return err
		}

		if _, exists := seen[key]; exists {
			return ErrInvalid
		}

		if _, want := names[key]; !want {
			return ErrInvalid
		}

		seen[key] = struct{}{}

		var drop json.RawMessage
		if err = dec.Decode(&drop); err != nil {
			return ErrInvalid
		}
	}

	return nil
}

// memberName reads the next member name of the object.
func memberName(dec *json.Decoder) (string, error) {
	tok, err := dec.Token()
	if err != nil {
		return "", ErrInvalid
	}

	key, ok := tok.(string)
	if !ok {
		return "", ErrInvalid
	}

	return key, nil
}

// wireNames returns the JSON object member names of the wire struct type.
func wireNames(wire any) (map[string]struct{}, error) {
	t := reflect.TypeOf(wire)
	if t == nil || t.Kind() != reflect.Pointer {
		return nil, ErrInvalid
	}

	t = t.Elem()
	if t.Kind() != reflect.Struct {
		return nil, ErrInvalid
	}

	names := make(map[string]struct{}, t.NumField())

	for f := range t.Fields() {
		if !f.IsExported() {
			continue
		}

		name := f.Name

		if tag, ok := f.Tag.Lookup("json"); ok {
			if tag == "-" {
				continue
			}

			name, _, _ = strings.Cut(tag, ",")
			if name == "" {
				name = f.Name
			}
		}

		names[name] = struct{}{}
	}

	return names, nil
}

// isCanonicalBase64URL reports whether token holds only bytes of the
// unpadded base64url alphabet. Defense in depth: the strict decode and the
// round-trip check below also reject the non-canonical forms; this check
// stays so that control bytes never reach the decoder.
func isCanonicalBase64URL(token string) bool {
	if strings.ContainsAny(token, "+/=\n\r\t ") {
		return false
	}

	for i := range token {
		c := token[i]
		if (c < 'A' || c > 'Z') && (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' && c != '_' {
			return false
		}
	}

	return true
}

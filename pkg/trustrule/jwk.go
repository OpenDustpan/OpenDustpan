package trustrule

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"slices"
)

// ErrKeySet reports a JWKS that the server cannot use.
var ErrKeySet = errors.New("the key set is not valid")

// Limits of a key set.
const (
	// MaxKeys is the most keys of one key set.
	MaxKeys = 100
	// MinRSABits is the smallest RSA modulus that a key can have.
	MinRSABits = 2048
	// maxRSABits is the largest RSA modulus that a key can have. A larger
	// modulus costs much time to check and no issuer uses one.
	maxRSABits = 8192
	// maxKeyIDLength is the longest key ID, in bytes.
	maxKeyIDLength = 256
)

// Key is one public signing key of a key set. ID is the kid of the key, or
// empty. Alg is the alg member of the key, or empty when the key names no
// algorithm. Public is a *rsa.PublicKey, an *ecdsa.PublicKey on P-256 or
// P-384, or an ed25519.PublicKey.
type Key struct {
	Public crypto.PublicKey
	ID     string
	Alg    string
}

// KeySet is the parsed public signing keys of one issuer.
type KeySet struct {
	keys []Key
}

// Len returns the number of signing keys of s.
func (s KeySet) Len() int {
	return len(s.keys)
}

// jwk is one key of a JWKS on the wire (RFC 7517). Only public members are
// read. The private members are named so that ParseKeySet can refuse a key
// that holds them.
type jwk struct {
	Kty string `json:"kty"`
	Use string `json:"use"`
	Alg string `json:"alg"`
	Kid string `json:"kid"`
	N   string `json:"n"`
	E   string `json:"e"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
	D   string `json:"d"`
	P   string `json:"p"`
	Q   string `json:"q"`
	DP  string `json:"dp"`
	DQ  string `json:"dq"`
	QI  string `json:"qi"`
	K   string `json:"k"`
	// KeyOps is the member key_ops, or nil when the key has none. The JSON
	// name is not camel case, so keyOpsOf reads it.
	KeyOps []string `json:"-"`
}

// keyOpsOf returns the member key_ops of the JSON object raw: nil when it is
// absent.
func keyOpsOf(raw json.RawMessage) ([]string, error) {
	var members map[string]json.RawMessage
	if err := json.Unmarshal(raw, &members); err != nil {
		return nil, fmt.Errorf("%w: a key is not a JSON object", ErrKeySet)
	}

	ops, ok := members["key_ops"]
	if !ok {
		return []string(nil), nil
	}

	var out []string
	if err := json.Unmarshal(ops, &out); err != nil || out == nil {
		return nil, fmt.Errorf("%w: key_ops is not a list of strings", ErrKeySet)
	}

	return out, nil
}

// ParseKeySet parses a JWKS (RFC 7517): a JSON object with the member
// "keys". It skips a key whose use is "enc", which is a key for encryption.
// It refuses the whole set, with ErrKeySet, when the set is not one JSON
// object, holds no signing key or more than MaxKeys keys, or when a key:
//   - holds a private member (d, p, q, dp, dq, qi or k),
//   - has a kty other than RSA, EC or OKP,
//   - is an RSA key below MinRSABits bits or with an exponent that is not
//     an odd number of at least 3,
//   - is an EC key that is not on P-256 or P-384,
//   - is an OKP key that is not Ed25519,
//   - has a use other than "sig", or key_ops without "verify",
//   - has an alg outside Algorithms, or an alg that does not fit the key,
//   - has a kid that another key of the set has, or a kid longer than 256
//     bytes.
//
// A set of more than one key needs a kid on each key, so that a token can
// name its key.
func ParseKeySet(raw []byte) (KeySet, error) {
	raws, err := decodeKeys(raw)
	if err != nil {
		return KeySet{keys: nil}, err
	}

	keys := make([]Key, 0, len(raws))

	for i, rawKey := range raws {
		key, use, keyErr := parseRawKey(rawKey)
		if keyErr != nil {
			return KeySet{keys: nil}, fmt.Errorf("key %d: %w", i, keyErr)
		}

		if use == useEncryption {
			continue
		}

		keys = append(keys, key)
	}

	if err := checkKeyIDs(keys); err != nil {
		return KeySet{keys: nil}, err
	}

	return KeySet{keys: keys}, nil
}

// useEncryption is the use of a key for encryption, which a key set skips.
const useEncryption = "enc"

// decodeKeys returns the raw keys of the JWKS raw.
func decodeKeys(raw []byte) ([]json.RawMessage, error) {
	var doc struct {
		Keys []json.RawMessage `json:"keys"`
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("%w: not a JSON object with keys", ErrKeySet)
	}

	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: data after the JSON object", ErrKeySet)
	}

	if len(doc.Keys) > MaxKeys {
		return nil, fmt.Errorf("%w: more than %d keys", ErrKeySet, MaxKeys)
	}

	return doc.Keys, nil
}

// parseRawKey returns the key of rawKey and its use. A key with the use
// useEncryption is not parsed further.
func parseRawKey(rawKey json.RawMessage) (Key, string, error) {
	none := Key{Public: nil, ID: "", Alg: ""}

	var k jwk
	if err := json.Unmarshal(rawKey, &k); err != nil {
		return none, "", fmt.Errorf("%w: a key is not a JSON object", ErrKeySet)
	}

	ops, err := keyOpsOf(rawKey)
	if err != nil {
		return none, "", err
	}

	k.KeyOps = ops

	if k.Use == useEncryption {
		return none, k.Use, nil
	}

	key, err := parseKey(&k)
	if err != nil {
		return none, "", err
	}

	return key, k.Use, nil
}

// checkKeyIDs returns ErrKeySet when keys is empty, when two keys have one
// kid, or when a set of more than one key has a key without a kid.
func checkKeyIDs(keys []Key) error {
	if len(keys) == 0 {
		return fmt.Errorf("%w: no signing key", ErrKeySet)
	}

	for i := range keys {
		if len(keys) > 1 && keys[i].ID == "" {
			return fmt.Errorf("%w: a set of more than one key needs a kid on each key", ErrKeySet)
		}

		for j := range i {
			if keys[j].ID == keys[i].ID {
				return fmt.Errorf("%w: two keys have the kid %q", ErrKeySet, keys[i].ID)
			}
		}
	}

	return nil
}

// parseKey returns the public key of k, or an error that wraps ErrKeySet and
// says why the key is not usable.
func parseKey(k *jwk) (Key, error) {
	none := Key{Public: nil, ID: "", Alg: ""}

	if err := checkMembers(k); err != nil {
		return none, err
	}

	pub, err := publicKey(k)
	if err != nil {
		return none, err
	}

	if k.Alg != "" && !algFits(k.Alg, pub) {
		return none, fmt.Errorf("%w: alg %q does not fit the key type", ErrKeySet, k.Alg)
	}

	return Key{Public: pub, ID: k.Kid, Alg: k.Alg}, nil
}

// checkMembers returns ErrKeySet for a key that holds a private member, that
// is not for signatures, or that has a kid or an alg that a key cannot have.
func checkMembers(k *jwk) error {
	if hasPrivate(k) {
		return fmt.Errorf("%w: the key holds private or secret material: give the public key only", ErrKeySet)
	}

	switch {
	case k.Use != "" && k.Use != "sig":
		return fmt.Errorf("%w: use %q: a signing key has use sig", ErrKeySet, k.Use)
	case k.KeyOps != nil && !slices.Contains(k.KeyOps, "verify"):
		return fmt.Errorf("%w: key_ops does not hold verify", ErrKeySet)
	case len(k.Kid) > maxKeyIDLength:
		return fmt.Errorf("%w: the kid is longer than %d bytes", ErrKeySet, maxKeyIDLength)
	case k.Alg != "" && !slices.Contains(Algorithms(), k.Alg):
		return fmt.Errorf("%w: alg %q is not an allowed algorithm", ErrKeySet, k.Alg)
	default:
		return nil
	}
}

// hasPrivate reports whether k holds a member of a private or a secret key.
func hasPrivate(k *jwk) bool {
	for _, member := range []string{k.D, k.P, k.Q, k.DP, k.DQ, k.QI, k.K} {
		if member != "" {
			return true
		}
	}

	return false
}

// publicKey returns the public key of k by its kty.
func publicKey(k *jwk) (crypto.PublicKey, error) {
	switch k.Kty {
	case "RSA":
		return rsaKey(k)
	case "EC":
		return ecKey(k)
	case "OKP":
		return okpKey(k)
	default:
		return nil, fmt.Errorf("%w: kty %q: use RSA, EC or OKP", ErrKeySet, k.Kty)
	}
}

// Bounds of an RSA exponent: an odd number from 3 to 2^31-1.
const (
	minRSAExponent = 3
	maxRSAExponent = 1<<31 - 1
)

// rsaKey returns the RSA public key of k.
func rsaKey(k *jwk) (*rsa.PublicKey, error) {
	n, err := b64Int("n", k.N)
	if err != nil {
		return nil, err
	}

	e, err := b64Int("e", k.E)
	if err != nil {
		return nil, err
	}

	if bits := n.BitLen(); bits < MinRSABits || bits > maxRSABits {
		return nil, fmt.Errorf("%w: an RSA key has %d to %d bits, not %d", ErrKeySet, MinRSABits, maxRSABits, bits)
	}

	if !e.IsInt64() || e.Int64() < minRSAExponent || e.Int64() > maxRSAExponent || e.Bit(0) == 0 {
		return nil, fmt.Errorf("%w: the RSA exponent is not an odd number from 3 to 2^31-1", ErrKeySet)
	}

	return &rsa.PublicKey{N: n, E: int(e.Int64())}, nil
}

// uncompressedPoint is the first byte of an uncompressed EC point (SEC 1).
const uncompressedPoint = 4

// ecKey returns the EC public key of k, on P-256 or P-384. The parse refuses
// a point that is not on the curve.
func ecKey(k *jwk) (*ecdsa.PublicKey, error) {
	var (
		curve elliptic.Curve
		size  int
	)

	switch k.Crv {
	case "P-256":
		curve, size = elliptic.P256(), 32
	case "P-384":
		curve, size = elliptic.P384(), 48
	default:
		return nil, fmt.Errorf("%w: crv %q: use P-256 or P-384", ErrKeySet, k.Crv)
	}

	x, err := b64Fixed("x", k.X, size)
	if err != nil {
		return nil, err
	}

	y, err := b64Fixed("y", k.Y, size)
	if err != nil {
		return nil, err
	}

	point := make([]byte, 0, 1+2*size)
	point = append(point, uncompressedPoint)
	point = append(point, x...)
	point = append(point, y...)

	pub, err := ecdsa.ParseUncompressedPublicKey(curve, point)
	if err != nil {
		return nil, fmt.Errorf("%w: the point is not on the curve", ErrKeySet)
	}

	return pub, nil
}

// okpKey returns the Ed25519 public key of k.
func okpKey(k *jwk) (ed25519.PublicKey, error) {
	// The crv of an Ed25519 key (RFC 8037) is the name of the scheme.
	if k.Crv != tls.Ed25519.String() {
		return nil, fmt.Errorf("%w: crv %q: use Ed25519", ErrKeySet, k.Crv)
	}

	x, err := b64Fixed("x", k.X, ed25519.PublicKeySize)
	if err != nil {
		return nil, err
	}

	return ed25519.PublicKey(x), nil
}

// b64Int decodes the member name: an unpadded base64url big-endian unsigned
// integer with no leading zero byte.
func b64Int(name, s string) (*big.Int, error) {
	b, err := base64.RawURLEncoding.Strict().DecodeString(s)
	if err != nil || len(b) == 0 {
		return nil, fmt.Errorf("%w: %s is not unpadded base64url", ErrKeySet, name)
	}

	if b[0] == 0 {
		return nil, fmt.Errorf("%w: %s has a leading zero byte", ErrKeySet, name)
	}

	return new(big.Int).SetBytes(b), nil
}

// b64Fixed decodes the member name: an unpadded base64url value of exactly
// size bytes.
func b64Fixed(name, s string, size int) ([]byte, error) {
	b, err := base64.RawURLEncoding.Strict().DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("%w: %s is not unpadded base64url", ErrKeySet, name)
	}

	if len(b) != size {
		return nil, fmt.Errorf("%w: %s has %d bytes, want %d", ErrKeySet, name, len(b), size)
	}

	return b, nil
}

// Has reports whether s holds a key with the ID kid.
func (s KeySet) Has(kid string) bool {
	for _, k := range s.keys {
		if k.ID == kid {
			return true
		}
	}

	return false
}

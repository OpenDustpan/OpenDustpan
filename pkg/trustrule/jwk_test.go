package trustrule_test

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math/big"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/trustrule"
)

// b64 encodes b as unpadded base64url.
func b64(b []byte) string {
	return base64.RawURLEncoding.EncodeToString(b)
}

// ed25519Set returns a JWKS with one new Ed25519 key with the ID kid.
func ed25519Set(t *testing.T, kid string) string {
	t.Helper()

	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	return fmt.Sprintf(`{"keys":[{"kty":"OKP","crv":"Ed25519","kid":%q,"x":%q}]}`, kid, b64(pub))
}

// rsa2048 returns a new RSA key of 2048 bits.
func rsa2048(t *testing.T) *rsa.PrivateKey {
	t.Helper()

	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	return k
}

// rsaJWK returns the JWK members of the public part of k.
func rsaJWK(k *rsa.PublicKey) map[string]any {
	return map[string]any{"kty": "RSA", "n": b64(k.N.Bytes()), "e": b64(big.NewInt(int64(k.E)).Bytes())}
}

// ecJWK returns the JWK members of the public part of k.
func ecJWK(t *testing.T, k *ecdsa.PublicKey, crv string, size int) map[string]any {
	t.Helper()

	raw, err := k.Bytes()
	if err != nil {
		t.Fatalf("encode point: %v", err)
	}

	return map[string]any{"kty": "EC", "crv": crv, "x": b64(raw[1 : 1+size]), "y": b64(raw[1+size:])}
}

// set returns the JWKS of keys.
func set(t *testing.T, keys ...map[string]any) string {
	t.Helper()

	b, err := json.Marshal(map[string]any{"keys": keys})
	if err != nil {
		t.Fatalf("encode set: %v", err)
	}

	return string(b)
}

// with returns a copy of m with the members of extra.
func with(m, extra map[string]any) map[string]any {
	out := maps.Clone(m)
	maps.Copy(out, extra)

	return out
}

func TestParseKeySetAcceptsEachKeyType(t *testing.T) {
	t.Parallel()

	p256, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	p384, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	rsaPub := rsaJWK(&rsa2048(t).PublicKey)

	raw := set(t,
		with(rsaPub, map[string]any{"kid": "rsa", "use": "sig", "alg": "RS256"}),
		with(ecJWK(t, &p256.PublicKey, "P-256", 32), map[string]any{"kid": "p256", "key_ops": []string{"verify"}}),
		with(ecJWK(t, &p384.PublicKey, "P-384", 48), map[string]any{"kid": "p384"}),
		// An encryption key is skipped: it is not a signing key.
		with(rsaPub, map[string]any{"kid": "enc", "use": "enc"}),
	)

	keys, err := trustrule.ParseKeySet([]byte(raw))
	if err != nil {
		t.Fatalf("ParseKeySet = %v", err)
	}

	if keys.Len() != 3 {
		t.Fatalf("keys = %d, want 3 signing keys", keys.Len())
	}
}

// TestParseKeySetRefuses checks the boundary of a JWKS: each set is refused
// with ErrKeySet.
func TestParseKeySetRefuses(t *testing.T) {
	t.Parallel()

	rsaPub := rsaJWK(&rsa2048(t).PublicKey)

	// A modulus of 1024 bits: the parse checks only its size, so no key of
	// that size is made.
	small := &rsa.PublicKey{N: new(big.Int).Lsh(big.NewInt(1), 1023), E: 65537}

	p224, err := ecdsa.GenerateKey(elliptic.P224(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	p256, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	offCurve := ecJWK(t, &p256.PublicKey, "P-256", 32)
	offCurve["y"] = b64(make([]byte, 32))

	manyKeys := make([]map[string]any, trustrule.MaxKeys+1)
	for i := range manyKeys {
		manyKeys[i] = with(rsaPub, map[string]any{"kid": fmt.Sprintf("k%d", i)})
	}

	cases := []struct {
		name string
		raw  string
	}{
		{"not JSON", "keys"},
		{"text after the set", set(t, rsaPub) + "{}"},
		{"no key", `{"keys":[]}`},
		{"only an encryption key", set(t, with(rsaPub, map[string]any{"use": "enc"}))},
		{"an RSA private key", set(t, with(rsaPub, map[string]any{"d": b64([]byte("private exponent"))}))},
		{"a secret key", `{"keys":[{"kty":"oct","k":"c2VjcmV0"}]}`},
		{"a symmetric key with no secret", `{"keys":[{"kty":"oct"}]}`},
		{"an RSA key of 1024 bits", set(t, rsaJWK(small))},
		{"an RSA exponent of 1", set(t, with(rsaPub, map[string]any{"e": "AQ"}))},
		{"an even RSA exponent", set(t, with(rsaPub, map[string]any{"e": "AQAA"}))},
		{"padded base64", set(t, with(rsaPub, map[string]any{"e": "AQAB=="}))},
		{"a curve that is not allowed", set(t, map[string]any{
			"kty": "EC", "crv": "P-224", "x": b64(p224.X.Bytes()), "y": b64(p224.Y.Bytes()),
		})},
		{"a point not on the curve", set(t, offCurve)},
		{"an X25519 key", set(t, map[string]any{"kty": "OKP", "crv": "X25519", "x": b64(make([]byte, 32))})},
		{"a short Ed25519 key", set(t, map[string]any{
			"kty": "OKP", "crv": tls.Ed25519.String(), "x": b64(make([]byte, 31)),
		})},
		{"a use that is not sig", set(t, with(rsaPub, map[string]any{"use": "other"}))},
		{"key_ops without verify", set(t, with(rsaPub, map[string]any{"key_ops": []string{"sign"}}))},
		{"key_ops that is not a list", set(t, with(rsaPub, map[string]any{"key_ops": "verify"}))},
		{"an alg of none", set(t, with(rsaPub, map[string]any{"alg": "none"}))},
		{"an HMAC alg", set(t, with(rsaPub, map[string]any{"alg": "HS256"}))},
		{"an alg that does not fit the key", set(t, with(rsaPub, map[string]any{"alg": "ES256"}))},
		{"two keys with one kid", set(t,
			with(rsaPub, map[string]any{"kid": "a"}), with(rsaPub, map[string]any{"kid": "a"}),
		)},
		{"two keys and one without a kid", set(t, with(rsaPub, map[string]any{"kid": "a"}), rsaPub)},
		{"a long kid", set(t, with(rsaPub, map[string]any{"kid": strings.Repeat("k", 257)}))},
		{"too many keys", set(t, manyKeys...)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if _, err := trustrule.ParseKeySet([]byte(tc.raw)); !errors.Is(err, trustrule.ErrKeySet) {
				t.Fatalf("ParseKeySet = %v, want ErrKeySet", err)
			}
		})
	}
}

// TestParseKeySetRefusesAnAlgOutsideTheAllowlist checks the alg allowlist of
// a key alone: the check that the alg fits the key type refuses the same
// keys, so the test asserts the error of the allowlist.
func TestParseKeySetRefusesAnAlgOutsideTheAllowlist(t *testing.T) {
	t.Parallel()

	rsaPub := rsaJWK(&rsa2048(t).PublicKey)

	for _, alg := range []string{"none", "HS256", "HS512", "RS1"} {
		t.Run("alg "+alg, func(t *testing.T) {
			t.Parallel()

			raw := set(t, with(rsaPub, map[string]any{"alg": alg}))

			_, err := trustrule.ParseKeySet([]byte(raw))
			if !errors.Is(err, trustrule.ErrKeySet) || !strings.Contains(err.Error(), "is not an allowed algorithm") {
				t.Fatalf("ParseKeySet = %v, want the error of the alg allowlist", err)
			}
		})
	}
}

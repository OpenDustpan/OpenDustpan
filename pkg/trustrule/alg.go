package trustrule

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"strings"
)

// Algorithms returns the signature algorithms that a key of a rule can have
// and that the exchange accepts. It never holds "none" or an HMAC algorithm:
// an HMAC key is a shared secret, and with a public key as the secret, anyone
// could sign.
func Algorithms() []string {
	return []string{"RS256", "RS384", "RS512", "PS256", "PS384", "PS512", "ES256", "ES384", "EdDSA"}
}

// algFits reports whether the public key pub fits the algorithm alg: an RSA
// key for RS and PS, a P-256 key for ES256, a P-384 key for ES384, and an
// Ed25519 key for EdDSA.
func algFits(alg string, pub crypto.PublicKey) bool {
	switch k := pub.(type) {
	case *rsa.PublicKey:
		return strings.HasPrefix(alg, "RS") || strings.HasPrefix(alg, "PS")
	case *ecdsa.PublicKey:
		return (alg == "ES256" && k.Curve == elliptic.P256()) || (alg == "ES384" && k.Curve == elliptic.P384())
	case ed25519.PublicKey:
		return alg == "EdDSA"
	default:
		return false
	}
}

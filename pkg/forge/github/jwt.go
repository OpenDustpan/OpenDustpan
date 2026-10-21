package github

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"strconv"
	"time"

	"opendustpan.org/opendustpan/pkg/forge"
)

// JWT timing. GitHub refuses a JWT with a lifetime above ten minutes, and
// a JWT with an issue time in the future. The issue time is 60 seconds in
// the past so that a clock that runs a little fast still passes.
const (
	jwtBackdate = 60 * time.Second
	jwtLifetime = 9 * time.Minute
)

// minKeyBits is the least size of the RSA key of a GitHub App. GitHub
// makes keys of 2048 bits.
const minKeyBits = 2048

// jwtHeader is the fixed header of the JWT that GitHub expects.
const jwtHeader = `{"alg":"RS256","typ":"JWT"}`

// jwtClaims are the claims of the JWT of a GitHub App.
type jwtClaims struct {
	Issuer   string `json:"iss"`
	IssuedAt int64  `json:"iat"`
	Expires  int64  `json:"exp"`
}

// parsePrivateKey reads an RSA private key in PEM form, PKCS#1 or PKCS#8.
// Its errors never hold a byte of the key.
func parsePrivateKey(secret []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(secret)
	if block == nil {
		return nil, fmt.Errorf("%w: the GitHub App key is not in PEM form", forge.ErrInvalid)
	}

	var key *rsa.PrivateKey

	switch block.Type {
	case "RSA PRIVATE KEY":
		k, err := x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("%w: the GitHub App key does not parse", forge.ErrInvalid)
		}

		key = k
	case "PRIVATE KEY":
		parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("%w: the GitHub App key does not parse", forge.ErrInvalid)
		}

		k, ok := parsed.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("%w: the GitHub App key is not an RSA key", forge.ErrInvalid)
		}

		key = k
	default:
		return nil, fmt.Errorf(
			"%w: the GitHub App key has the PEM type %q, not a private key",
			forge.ErrInvalid,
			block.Type,
		)
	}

	if key.N.BitLen() < minKeyBits {
		return nil, fmt.Errorf("%w: the GitHub App key has fewer than %d bits", forge.ErrInvalid, minKeyBits)
	}

	if err := key.Validate(); err != nil {
		return nil, fmt.Errorf("%w: the GitHub App key is not valid", forge.ErrInvalid)
	}

	return key, nil
}

// signJWT returns the JWT of the app with the ID appID, signed with key
// (RS256). It uses only the standard library.
func signJWT(key *rsa.PrivateKey, appID string, now time.Time) (string, error) {
	claims, err := json.Marshal(jwtClaims{
		Issuer:   appID,
		IssuedAt: now.Add(-jwtBackdate).Unix(),
		Expires:  now.Add(jwtLifetime).Unix(),
	})
	if err != nil {
		return "", fmt.Errorf("%w: GitHub App claims do not encode", forge.ErrInvalid)
	}

	enc := base64.RawURLEncoding
	signing := enc.EncodeToString([]byte(jwtHeader)) + "." + enc.EncodeToString(claims)
	digest := sha256.Sum256([]byte(signing))

	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		return "", fmt.Errorf("%w: the GitHub App key cannot sign", forge.ErrInvalid)
	}

	return signing + "." + enc.EncodeToString(sig), nil
}

// validID reports whether id is a decimal number of 1 to 20 digits with no
// leading zero.
func validID(id string) bool {
	if id == "" || len(id) > 20 || id[0] == '0' {
		return false
	}

	_, err := strconv.ParseUint(id, 10, 64)

	return err == nil
}

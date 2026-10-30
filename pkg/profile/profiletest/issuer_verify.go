package profiletest

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"
)

// ErrToken reports a token that a relying party refuses. The message says why.
var ErrToken = errors.New("token refused")

// Expect is what a relying party expects of a token.
type Expect struct {
	Now      time.Time
	Issuer   string
	Audience string
}

// VerifyToken checks a compact JWT against the JWKS document jwks, as a cloud
// does, and returns its claims. It is independent of the code of the issuer:
// it uses only the standard library, and it accepts RS256 and nothing else.
// So it refuses alg none, an HMAC algorithm that uses a public key as its
// secret, a key it does not find by kid, a bad signature, a wrong issuer, a
// wrong audience, an expired token and a token that is not yet valid.
func VerifyToken(jwks []byte, token string, want Expect) (map[string]any, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("%w: a compact JWT has three parts", ErrToken)
	}

	var header struct {
		Alg string `json:"alg"`
		Typ string `json:"typ"`
		Kid string `json:"kid"`
	}

	if err := decodePart(parts[0], &header); err != nil {
		return nil, err
	}

	if header.Alg != "RS256" {
		return nil, fmt.Errorf("%w: algorithm %q, want RS256", ErrToken, header.Alg)
	}

	if header.Kid == "" {
		return nil, fmt.Errorf("%w: the header has no kid", ErrToken)
	}

	pub, err := findKey(jwks, header.Kid)
	if err != nil {
		return nil, err
	}

	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, fmt.Errorf("%w: the signature does not decode", ErrToken)
	}

	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if verr := rsa.VerifyPKCS1v15(pub, crypto.SHA256, sum[:], sig); verr != nil {
		return nil, fmt.Errorf("%w: the signature is wrong", ErrToken)
	}

	claims := map[string]any{}
	if cerr := decodePart(parts[1], &claims); cerr != nil {
		return nil, cerr
	}

	return claims, checkClaims(claims, want)
}

func decodePart(part string, into any) error {
	raw, err := base64.RawURLEncoding.DecodeString(part)
	if err != nil {
		return fmt.Errorf("%w: a part does not decode", ErrToken)
	}

	if err = json.Unmarshal(raw, into); err != nil {
		return fmt.Errorf("%w: a part is not JSON", ErrToken)
	}

	return nil
}

func findKey(jwks []byte, kid string) (*rsa.PublicKey, error) {
	var doc struct {
		Keys []struct {
			Kty string `json:"kty"`
			Use string `json:"use"`
			Alg string `json:"alg"`
			Kid string `json:"kid"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}

	if err := json.Unmarshal(jwks, &doc); err != nil {
		return nil, fmt.Errorf("%w: the JWKS is not JSON", ErrToken)
	}

	for _, k := range doc.Keys {
		if k.Kid != kid {
			continue
		}

		if k.Kty != "RSA" || (k.Alg != "" && k.Alg != "RS256") || (k.Use != "" && k.Use != "sig") {
			return nil, fmt.Errorf("%w: key %s is not an RS256 signing key", ErrToken, kid)
		}

		n, nerr := base64.RawURLEncoding.DecodeString(k.N)
		e, eerr := base64.RawURLEncoding.DecodeString(k.E)

		if nerr != nil || eerr != nil || len(e) == 0 || len(e) > 4 {
			return nil, fmt.Errorf("%w: key %s does not decode", ErrToken, kid)
		}

		return &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(new(big.Int).SetBytes(e).Int64())}, nil
	}

	return nil, fmt.Errorf("%w: no key %s in the JWKS", ErrToken, kid)
}

func checkClaims(c map[string]any, want Expect) error {
	if iss, _ := c["iss"].(string); iss != want.Issuer {
		return fmt.Errorf("%w: issuer %q, want %q", ErrToken, iss, want.Issuer)
	}

	// One string. A list of audiences is refused, so a token for several
	// relying parties never passes as a token for one.
	if aud, ok := c["aud"].(string); !ok || aud != want.Audience {
		return fmt.Errorf("%w: audience %v, want %q", ErrToken, c["aud"], want.Audience)
	}

	if sub, _ := c["sub"].(string); sub == "" {
		return fmt.Errorf("%w: no sub", ErrToken)
	}

	if jti, _ := c["jti"].(string); !strings.HasPrefix(jti, "oit-") {
		return fmt.Errorf("%w: no jti", ErrToken)
	}

	exp, expOK := c["exp"].(float64)
	iat, iatOK := c["iat"].(float64)
	nbf, nbfOK := c["nbf"].(float64)

	if !expOK || !iatOK || !nbfOK {
		return fmt.Errorf("%w: exp, iat and nbf are numbers", ErrToken)
	}

	now := float64(want.Now.Unix())

	switch {
	case now >= exp:
		return fmt.Errorf("%w: the token expired", ErrToken)
	case now < nbf:
		return fmt.Errorf("%w: the token is not valid yet", ErrToken)
	case exp-iat > 3600 || exp <= iat:
		return fmt.Errorf("%w: the lifetime is out of bounds", ErrToken)
	default:
		return nil
	}
}

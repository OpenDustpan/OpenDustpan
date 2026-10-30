package profile

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"math/big"
	"slices"
	"strings"
)

// JWK is one public key of the JWKS, as RFC 7517 and RFC 7518 give it.
type JWK struct {
	Kty string `json:"kty"`
	Use string `json:"use"`
	Alg string `json:"alg"`
	Kid string `json:"kid"`
	N   string `json:"n"`
	E   string `json:"e"`
}

// JWKS is the document at the jwks_uri of the issuer: public keys only.
type JWKS struct {
	Keys []JWK `json:"keys"`
}

// PublicKey parses the public key of k. It refuses a key that is not RSA of
// KeyBits.
func (k Key) PublicKey() (*rsa.PublicKey, error) {
	parsed, err := x509.ParsePKIXPublicKey(k.Public)
	if err != nil {
		return nil, fmt.Errorf("%w: public key of %s: %w", ErrInvalid, k.ID, err)
	}

	pub, ok := parsed.(*rsa.PublicKey)
	if !ok || pub.N.BitLen() != KeyBits {
		return nil, fmt.Errorf("%w: key %s is not an RSA key of %d bits", ErrInvalid, k.ID, KeyBits)
	}

	return pub, nil
}

// JWK returns the JWK of k.
func (k Key) JWK() (JWK, error) {
	pub, err := k.PublicKey()
	if err != nil {
		return JWK{}, err
	}

	return JWK{
		Kty: "RSA", Use: "sig", Alg: KeyAlgorithm, Kid: string(k.ID),
		N: base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
		E: base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
	}, nil
}

// BuildJWKS returns the JWKS of the keys that are published: the states next,
// active and retired. The order is fixed: active, next, then the retired keys
// from the newest. A key of another state is left out, so a caller that passes
// a deleted key still publishes nothing of it.
func BuildJWKS(keys []Key) (JWKS, error) {
	rank := map[KeyState]int{KeyActive: 0, KeyNext: 1, KeyRetired: 2}
	pub := slices.DeleteFunc(slices.Clone(keys), func(k Key) bool { return !k.State.Published() })

	slices.SortStableFunc(pub, func(a, b Key) int {
		if rank[a.State] != rank[b.State] {
			return rank[a.State] - rank[b.State]
		}

		return b.CreatedAt.Compare(a.CreatedAt)
	})

	out := JWKS{Keys: make([]JWK, 0, len(pub))}

	for _, k := range pub {
		jwk, err := k.JWK()
		if err != nil {
			return JWKS{}, err
		}

		out.Keys = append(out.Keys, jwk)
	}

	return out, nil
}

// Discovery is the OpenID Connect discovery document of the issuer of a
// namespace. It holds public data only.
type Discovery struct {
	Issuer                           string   `json:"issuer"`
	JWKSURI                          string   `json:"jwks_uri"`
	ResponseTypesSupported           []string `json:"response_types_supported"`
	SubjectTypesSupported            []string `json:"subject_types_supported"`
	IDTokenSigningAlgValuesSupported []string `json:"id_token_signing_alg_values_supported"`
	ScopesSupported                  []string `json:"scopes_supported"`
	ClaimsSupported                  []string `json:"claims_supported"`
}

// NewDiscovery returns the discovery document for the issuer URL issuer. The
// jwks_uri is the issuer URL and "/jwks", for the default URL and for an
// issuerURL of a namespace.
func NewDiscovery(issuer string) Discovery {
	return Discovery{
		Issuer: issuer, JWKSURI: strings.TrimSuffix(issuer, "/") + "/jwks",
		ResponseTypesSupported: []string{"id_token"}, SubjectTypesSupported: []string{"public"},
		IDTokenSigningAlgValuesSupported: []string{KeyAlgorithm}, ScopesSupported: []string{"openid"},
		ClaimsSupported: ClaimsSupported(),
	}
}

// awsTagsClaim is the one claim name that is not snake_case.
const awsTagsClaim = "https://aws.amazon.com/tags"

// registeredClaims are the claims that the issuer sets itself and that the
// claims of an open never replace.
func registeredClaims() []string {
	return []string{"iss", "sub", "aud", "iat", "nbf", "exp", "jti", "namespace", "namespace_id", "purpose"}
}

// ClaimsSupported lists the claims that a token can hold. The list of the
// claims of an open follows the claim set of the server.
func ClaimsSupported() []string {
	return slices.Concat(registeredClaims(), []string{
		"profile", "profile_id", "profile_revision", "root_profile", "root_profile_id", "access", "action",
		"tool_action", "phase", "tool", "unit", "unit_id", "unit_ref", "collection", "collection_id", "actor_type",
		"actor_id", "actor", "act", "trigger", "job", "change_order", "change_order_revision", "runner_pool",
		"runner_pool_id", "repository", "ref", "ref_type", "ref_protected", "commit", "source", "body_sha256",
		awsTagsClaim,
	})
}

// CheckClaimName reports whether name may be a claim of an open: snake_case,
// or the AWS tags claim, and not a claim that the issuer sets itself.
func CheckClaimName(name string) error {
	if slices.Contains(registeredClaims(), name) {
		return fmt.Errorf("%w: the claim %q is set by the issuer", ErrInvalid, name)
	}

	if name == awsTagsClaim {
		return nil
	}

	if name == "" || len(name) > 64 || name[0] < 'a' || name[0] > 'z' {
		return fmt.Errorf("%w: the claim name %q is not snake_case", ErrInvalid, name)
	}

	for _, r := range name {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' {
			return fmt.Errorf("%w: the claim name %q is not snake_case", ErrInvalid, name)
		}
	}

	return nil
}

// CheckAudience checks an audience as a token needs it: 1 to MaxAudienceBytes
// bytes, valid UTF-8, no control character, and not the publicURL of the
// installation or a URL under it, so that a token of the issuer never
// authenticates to OpenDustpan. publicURL is empty when the installation has
// none. The server refuses a value and does not change it.
func CheckAudience(aud, publicURL string) error {
	if aud == "" || len(aud) > MaxAudienceBytes {
		return fmt.Errorf("%w: an audience has 1 to %d bytes", ErrInvalid, MaxAudienceBytes)
	}

	for _, r := range aud {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) || r == 0xfffd {
			return fmt.Errorf("%w: an audience holds a control or invalid character", ErrInvalid)
		}
	}

	if publicURL == "" {
		return nil
	}

	pub := strings.ToLower(strings.TrimRight(publicURL, "/"))
	got := strings.ToLower(strings.TrimRight(aud, "/"))

	if got == pub || strings.HasPrefix(got, pub+"/") || strings.HasPrefix(got, pub+"?") ||
		strings.HasPrefix(got, pub+"#") {
		return fmt.Errorf("%w: the audience is the address of this server or under it", ErrInvalid)
	}

	return nil
}

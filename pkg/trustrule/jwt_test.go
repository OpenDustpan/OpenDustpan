package trustrule_test

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/trustrule"
)

// signer signs JWTs for the tests with one key.
type signer struct {
	sign func(input []byte) []byte
	jwk  map[string]any
	alg  string
}

// rsaSigner returns a signer of RS256 with a new key of 2048 bits.
func rsaSigner(t *testing.T, kid string) signer {
	t.Helper()

	k := rsa2048(t)

	return signer{
		alg: "RS256",
		jwk: with(rsaJWK(&k.PublicKey), map[string]any{"kid": kid}),
		sign: func(input []byte) []byte {
			digest := sha256.Sum256(input)

			sig, err := rsa.SignPKCS1v15(rand.Reader, k, crypto.SHA256, digest[:])
			if err != nil {
				t.Fatalf("sign: %v", err)
			}

			return sig
		},
	}
}

// ecSigner returns a signer of ES256 with a new P-256 key.
func ecSigner(t *testing.T, kid string) signer {
	t.Helper()

	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	return signer{
		alg: "ES256",
		jwk: with(ecJWK(t, &k.PublicKey, "P-256", 32), map[string]any{"kid": kid}),
		sign: func(input []byte) []byte {
			digest := sha256.Sum256(input)

			r, s, signErr := ecdsa.Sign(rand.Reader, k, digest[:])
			if signErr != nil {
				t.Fatalf("sign: %v", signErr)
			}

			out := make([]byte, 64)
			r.FillBytes(out[:32])
			s.FillBytes(out[32:])

			return out
		},
	}
}

// edSigner returns a signer of EdDSA with a new Ed25519 key.
func edSigner(t *testing.T, kid string) signer {
	t.Helper()

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	return signer{
		alg:  "EdDSA",
		jwk:  map[string]any{"kty": "OKP", "crv": tls.Ed25519.String(), "x": b64(pub), "kid": kid},
		sign: func(input []byte) []byte { return ed25519.Sign(priv, input) },
	}
}

// encode returns the base64url JSON of v.
func encode(t *testing.T, v any) string {
	t.Helper()

	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	return b64(b)
}

// token returns a JWT with the given header and claims, signed by s.
func (s signer) token(t *testing.T, header, claims map[string]any) string {
	t.Helper()

	input := encode(t, header) + "." + encode(t, claims)

	return input + "." + b64(s.sign([]byte(input)))
}

// header returns the usual header of s.
func (s signer) header() map[string]any {
	return map[string]any{"alg": s.alg, "typ": "JWT", "kid": s.jwk["kid"]}
}

// keys returns the key set of the signers.
func keys(t *testing.T, signers ...signer) trustrule.KeySet {
	t.Helper()

	jwks := make([]map[string]any, 0, len(signers))
	for _, s := range signers {
		jwks = append(jwks, s.jwk)
	}

	ks, err := trustrule.ParseKeySet([]byte(set(t, jwks...)))
	if err != nil {
		t.Fatalf("key set: %v", err)
	}

	return ks
}

// testTime returns the clock of the time checks.
func testTime() time.Time {
	return time.Unix(1_700_000_000, 0).UTC()
}

// claimsAt returns valid claims of a token made at now.
func claimsAt(now time.Time) map[string]any {
	return map[string]any{
		"iss": "https://token.actions.example.com", "aud": "https://dustpan.example.com",
		"sub": "repo:acme/infra:ref:refs/heads/main", "repository": "acme/infra",
		"iat": now.Unix(), "nbf": now.Unix(), "exp": now.Add(10 * time.Minute).Unix(), "jti": "run-1",
	}
}

// claims returns valid claims at testTime.
func claims() map[string]any {
	return claimsAt(testTime())
}

func TestVerifyAcceptsEachAlgorithmFamily(t *testing.T) {
	t.Parallel()

	for _, s := range []signer{rsaSigner(t, "rsa"), ecSigner(t, "ec"), edSigner(t, "ed")} {
		ks := keys(t, s)

		tok, err := trustrule.Verify(s.token(t, s.header(), claims()), ks)
		if err != nil {
			t.Fatalf("%s: Verify = %v", s.alg, err)
		}

		if tok.Alg != s.alg || tok.Subject() != "repo:acme/infra:ref:refs/heads/main" || tok.ID() != "run-1" {
			t.Errorf("%s: token = %+v", s.alg, tok)
		}
	}
}

// TestVerifyRefusesAnAlgorithmOutsideTheAllowlist sends tokens with alg none
// and with HMAC, also an HMAC whose secret is the public key of the issuer:
// the confusion of an HMAC key with an RSA key.
func TestVerifyRefusesAnAlgorithmOutsideTheAllowlist(t *testing.T) {
	t.Parallel()

	s := rsaSigner(t, "rsa")
	ks := keys(t, s)
	body := encode(t, claims())

	none := encode(t, map[string]any{"alg": "none", "kid": "rsa"}) + "." + body + "."
	noneSigned := encode(t, map[string]any{"alg": "none", "kid": "rsa"}) + "." + body + "." + b64([]byte("x"))

	// The secret of the HMAC is the JSON of the public key, as an attacker
	// who reads the JWKS would use it.
	secret, err := json.Marshal(s.jwk)
	if err != nil {
		t.Fatalf("encode key: %v", err)
	}

	hsInput := encode(t, map[string]any{"alg": "HS256", "kid": "rsa", "typ": "JWT"}) + "." + body
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(hsInput))
	hs := hsInput + "." + b64(mac.Sum(nil))

	for name, raw := range map[string]string{"none": none, "none with a signature": noneSigned, "HS256": hs} {
		if _, err := trustrule.Verify(raw, ks); !errors.Is(err, trustrule.ErrToken) {
			t.Errorf("%s: Verify = %v, want ErrToken", name, err)
		}
	}
}

// TestVerifyRefusesAKeyThatDoesNotFitTheAlgorithm signs with a key of one
// type and names the algorithm of another type.
func TestVerifyRefusesAKeyThatDoesNotFitTheAlgorithm(t *testing.T) {
	t.Parallel()

	ec := ecSigner(t, "ec")
	ks := keys(t, ec)

	h := ec.header()
	h["alg"] = "RS256"

	if _, err := trustrule.Verify(ec.token(t, h, claims()), ks); !errors.Is(err, trustrule.ErrToken) {
		t.Fatalf("Verify = %v, want ErrToken", err)
	}

	// A key that names its own algorithm accepts no other algorithm.
	rs := rsaSigner(t, "rsa")
	rs.jwk["alg"] = "RS384"
	ks = keys(t, rs)

	if _, err := trustrule.Verify(rs.token(t, rs.header(), claims()), ks); !errors.Is(err, trustrule.ErrToken) {
		t.Fatalf("Verify with another alg of the key = %v, want ErrToken", err)
	}
}

// TestVerifyRefusesAHeaderThatNamesAKey checks that a key or a key URL in the
// token never takes the place of the keys of the rule.
func TestVerifyRefusesAHeaderThatNamesAKey(t *testing.T) {
	t.Parallel()

	s := ecSigner(t, "ec")
	ks := keys(t, s)

	for _, member := range []string{"jku", "jwk", "x5u", "x5c", "crit", "zip", "b64"} {
		h := s.header()
		h[member] = "https://attacker.example.com/keys"

		if _, err := trustrule.Verify(s.token(t, h, claims()), ks); !errors.Is(err, trustrule.ErrToken) {
			t.Errorf("header member %s: Verify = %v, want ErrToken", member, err)
		}
	}

	h := s.header()
	h["typ"] = "at+jwt"

	if _, err := trustrule.Verify(s.token(t, h, claims()), ks); !errors.Is(err, trustrule.ErrToken) {
		t.Errorf("typ at+jwt: Verify = %v, want ErrToken", err)
	}
}

func TestVerifyRefusesABrokenToken(t *testing.T) {
	t.Parallel()

	s := ecSigner(t, "ec")
	other := ecSigner(t, "other")
	ks := keys(t, s)
	good := s.token(t, s.header(), claims())
	parts := strings.Split(good, ".")

	tampered := claims()
	tampered["sub"] = "repo:evil/infra:ref:refs/heads/main"

	unknownKid := s.header()
	unknownKid["kid"] = "nope"

	cases := map[string]string{
		"two parts":                    parts[0] + "." + parts[1],
		"four parts":                   good + ".x",
		"padded part":                  parts[0] + "=." + parts[1] + "." + parts[2],
		"a part that is not base64url": parts[0] + ".a+b." + parts[2],
		"another payload":              parts[0] + "." + encode(t, tampered) + "." + parts[2],
		"the key of another issuer":    other.token(t, map[string]any{"alg": "ES256", "kid": "ec"}, claims()),
		"an unknown kid":               s.token(t, unknownKid, claims()),
		"a short signature":            parts[0] + "." + parts[1] + "." + b64([]byte("short")),
		"a member twice": b64(
			[]byte(`{"alg":"ES256","kid":"ec","kid":"x"}`),
		) + "." + parts[1] + "." + parts[2],
		"too long": strings.Repeat("a", trustrule.MaxTokenBytes+1),
	}

	for name, raw := range cases {
		if _, err := trustrule.Verify(raw, ks); !errors.Is(err, trustrule.ErrToken) {
			t.Errorf("%s: Verify = %v, want ErrToken", name, err)
		}
	}

	// A set of two keys needs a kid in the token.
	two := keys(t, s, other)
	h := s.header()
	delete(h, "kid")

	if _, err := trustrule.Verify(s.token(t, h, claims()), two); !errors.Is(err, trustrule.ErrToken) {
		t.Errorf("no kid with two keys: Verify = %v, want ErrToken", err)
	}

	// A set of one key takes a token with no kid.
	if _, err := trustrule.Verify(s.token(t, h, claims()), ks); err != nil {
		t.Errorf("no kid with one key: Verify = %v, want nil", err)
	}
}

// verified returns the verified token of claims c, signed with a new key.
func verified(t *testing.T, c map[string]any) trustrule.Token {
	t.Helper()

	s := edSigner(t, "ed")

	tok, err := trustrule.Verify(s.token(t, s.header(), c), keys(t, s))
	if err != nil {
		t.Fatalf("Verify = %v", err)
	}

	return tok
}

// TestCheckTimeHoldsTheSkew checks exp, nbf and iat at the edges of the
// clock skew of 60 seconds.
func TestCheckTimeHoldsTheSkew(t *testing.T) {
	t.Parallel()

	now := testTime()
	skew := trustrule.ClockSkew

	cases := []struct {
		name   string
		change func(c map[string]any)
		ok     bool
	}{
		{"valid", func(map[string]any) {}, true},
		{"expired inside the skew", func(c map[string]any) { c["exp"] = now.Add(-skew + time.Second).Unix() }, true},
		{"expired at the skew", func(c map[string]any) { c["exp"] = now.Add(-skew).Unix() }, false},
		{"expired an hour ago", func(c map[string]any) { c["exp"] = now.Add(-time.Hour).Unix() }, false},
		{"no exp", func(c map[string]any) { delete(c, "exp") }, false},
		{"exp as text", func(c map[string]any) { c["exp"] = "never" }, false},
		{"exp negative", func(c map[string]any) { c["exp"] = -1 }, false},
		{"exp too far", func(c map[string]any) {
			c["exp"] = now.Add(trustrule.MaxTokenLifetime + time.Minute).Unix()
		}, false},
		{"nbf inside the skew", func(c map[string]any) { c["nbf"] = now.Add(skew).Unix() }, true},
		{"nbf after the skew", func(c map[string]any) { c["nbf"] = now.Add(skew + time.Second).Unix() }, false},
		{"iat after the skew", func(c map[string]any) { c["iat"] = now.Add(skew + time.Second).Unix() }, false},
		{"no nbf and no iat", func(c map[string]any) { delete(c, "nbf"); delete(c, "iat") }, true},
	}

	for _, tc := range cases {
		c := claimsAt(now)
		tc.change(c)
		tok := verified(t, c)

		err := tok.CheckTime(now)
		if tc.ok && err != nil {
			t.Errorf("%s: CheckTime = %v, want nil", tc.name, err)
		}

		if !tc.ok && !errors.Is(err, trustrule.ErrToken) {
			t.Errorf("%s: CheckTime = %v, want ErrToken", tc.name, err)
		}
	}
}

// TestCheckIssuerIsExact checks that the issuer matches with no case
// folding and no trailing slash.
func TestCheckIssuerIsExact(t *testing.T) {
	t.Parallel()

	tok := verified(t, claimsAt(testTime()))

	if err := tok.CheckIssuer("https://token.actions.example.com"); err != nil {
		t.Errorf("CheckIssuer = %v, want nil", err)
	}

	for _, other := range []string{
		"https://token.actions.example.com/", "https://TOKEN.actions.example.com", "https://token.actions.example",
		"",
	} {
		if err := tok.CheckIssuer(other); !errors.Is(err, trustrule.ErrToken) {
			t.Errorf("CheckIssuer(%q) = %v, want ErrToken", other, err)
		}
	}
}

// TestCheckAudienceIsExact checks the audience as a string and as a list.
func TestCheckAudienceIsExact(t *testing.T) {
	t.Parallel()

	const want = "https://dustpan.example.com"

	cases := []struct {
		aud any
		ok  bool
	}{
		{want, true},
		{[]string{"https://other.example.com", want}, true},
		{want + "/", false},
		{"https://DUSTPAN.example.com", false},
		{"https://dustpan.example.com.evil.example", false},
		{[]string{"https://other.example.com"}, false},
		{[]string{}, false},
		{42, false},
		{nil, false},
	}

	for _, tc := range cases {
		c := claimsAt(testTime())
		c["aud"] = tc.aud

		if tc.aud == nil {
			delete(c, "aud")
		}

		tok := verified(t, c)
		err := tok.CheckAudience(want)
		if tc.ok != (err == nil) {
			t.Errorf("aud %v: CheckAudience = %v, want ok %v", tc.aud, err, tc.ok)
		}
	}
}

// TestMatchesHasNoWildcard checks the conditions of a rule: exact values
// only, a star matches only a star, and a claim that is not a string matches
// nothing.
func TestMatchesHasNoWildcard(t *testing.T) {
	t.Parallel()

	r := validRule()
	r.Conditions = []trustrule.Condition{
		{Claim: "ref", Values: []string{"refs/heads/main", "refs/heads/*"}},
		{Claim: "sub", Values: []string{"repo:acme/infra:*"}},
	}

	cases := []struct {
		name string
		sub  any
		ref  any
		ok   bool
	}{
		{"the literal star", "repo:acme/infra:*", "refs/heads/main", true},
		{"a star does not match a branch", "repo:acme/infra:ref:refs/heads/main", "refs/heads/main", false},
		{"the literal star in ref", "repo:acme/infra:*", "refs/heads/*", true},
		{"a star does not match a ref", "repo:acme/infra:*", "refs/heads/evil", false},
		{"case differs", "REPO:acme/infra:*", "refs/heads/main", false},
		{"a list is not a string", []string{"repo:acme/infra:*"}, "refs/heads/main", false},
		{"a number is not a string", 7, "refs/heads/main", false},
		{"a missing claim", nil, "refs/heads/main", false},
	}

	for _, tc := range cases {
		c := claimsAt(testTime())
		c["sub"], c["ref"] = tc.sub, tc.ref

		if tc.sub == nil {
			delete(c, "sub")
		}

		tok := verified(t, c)
		if got := r.Matches(&tok); got != tc.ok {
			t.Errorf("%s: Matches = %v, want %v", tc.name, got, tc.ok)
		}
	}

	// Defense in depth: a rule that skipped Validate and has no condition on
	// the subject matches no token.
	r.Conditions = []trustrule.Condition{{Claim: "ref", Values: []string{"refs/heads/main"}}}

	tok := verified(t, claimsAt(testTime()))
	if r.Matches(&tok) {
		t.Error("a rule with no condition on the subject matched")
	}

	r.Conditions = nil
	if r.Matches(&tok) {
		t.Error("a rule with no condition matched")
	}
}

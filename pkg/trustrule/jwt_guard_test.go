package trustrule_test

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/trustrule"
)

// rawToken returns a JWT with the given header and the payload text raw,
// signed by s. It lets a test send a payload that json.Marshal never makes.
func (s signer) rawToken(t *testing.T, header map[string]any, raw string) string {
	t.Helper()

	input := encode(t, header) + "." + b64([]byte(raw))

	return input + "." + b64(s.sign([]byte(input)))
}

// TestVerifyHoldsItsAlgorithmAllowlist checks the alg allowlist of Verify
// alone: the key of the token is an RSA key with no alg of its own, so only
// the allowlist refuses an alg that starts like an RSA alg. A token with such
// an alg must not reach the signature check.
func TestVerifyHoldsItsAlgorithmAllowlist(t *testing.T) {
	t.Parallel()

	s := rsaSigner(t, "guard-rsa")
	ks := keys(t, s)

	for _, alg := range []string{"PS", "RS", "RS1", "PS1", "RS2560"} {
		h := s.header()
		h["alg"] = alg

		_, err := trustrule.Verify(s.token(t, h, claims()), ks)
		if !errors.Is(err, trustrule.ErrToken) || !strings.Contains(err.Error(), "is not allowed") {
			t.Errorf("alg %q: Verify = %v, want the error of the allowlist", alg, err)
		}
	}
}

// TestVerifyRefusesAPayloadMemberTwiceOrTrailingData checks the decode of
// the payload of a token with a valid signature.
func TestVerifyRefusesAPayloadMemberTwiceOrTrailingData(t *testing.T) {
	t.Parallel()

	s := edSigner(t, "guard-ed")
	ks := keys(t, s)

	body, err := json.Marshal(claims())
	if err != nil {
		t.Fatalf("encode claims: %v", err)
	}

	valid := string(body)

	cases := map[string]string{
		"a member twice": strings.TrimSuffix(valid, "}") + `,"sub":"repo:evil/fork:ref:refs/heads/main"}`,
		"trailing data":  valid + `{}`,
		"trailing text":  valid + `x`,
	}

	if _, err := trustrule.Verify(s.rawToken(t, s.header(), valid), ks); err != nil {
		t.Fatalf("Verify of the valid payload = %v", err)
	}

	for name, raw := range cases {
		if _, err := trustrule.Verify(s.rawToken(t, s.header(), raw), ks); !errors.Is(err, trustrule.ErrToken) {
			t.Errorf("%s: Verify = %v, want ErrToken", name, err)
		}
	}
}

// TestTokenPayloadRefusesInvalidUTF8 checks that a signed payload with a byte
// that is not UTF-8 in the iss claim is refused before the claim compare.
func TestTokenPayloadRefusesInvalidUTF8(t *testing.T) {
	t.Parallel()

	s := edSigner(t, "guard-ed")
	ks := keys(t, s)
	issuer := "https://token.actions.example.com"

	c := claims()
	c["iss"] = issuer

	body, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("encode claims: %v", err)
	}

	raw := s.rawToken(t, s.header(), strings.Replace(string(body), issuer, issuer+"\xff", 1))

	if _, verifyErr := trustrule.Verify(raw, ks); !errors.Is(verifyErr, trustrule.ErrToken) ||
		!strings.Contains(verifyErr.Error(), "the payload is not UTF-8") {
		t.Errorf("Verify = %v, want ErrToken with the payload is not UTF-8", verifyErr)
	}

	if _, issuerErr := trustrule.UnverifiedIssuer(raw); !errors.Is(issuerErr, trustrule.ErrToken) ||
		!strings.Contains(issuerErr.Error(), "the payload is not UTF-8") {
		t.Errorf("UnverifiedIssuer = %v, want ErrToken with the payload is not UTF-8", issuerErr)
	}
}

// TestTokenPayloadAcceptsValidUTF8 checks that a signed payload with valid
// UTF-8 in the iss claim still passes the check of the issuer.
func TestTokenPayloadAcceptsValidUTF8(t *testing.T) {
	t.Parallel()

	s := edSigner(t, "guard-ed")
	ks := keys(t, s)
	issuer := "https://token.actions.example.com"
	valid := issuer + "\ufffd"

	c := claims()
	c["iss"] = issuer

	body, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("encode claims: %v", err)
	}

	raw := s.rawToken(t, s.header(), strings.Replace(string(body), issuer, valid, 1))

	tok, err := trustrule.Verify(raw, ks)
	if err != nil {
		t.Fatalf("Verify = %v, want nil", err)
	}

	if err = tok.CheckIssuer(valid); err != nil {
		t.Errorf("CheckIssuer = %v, want nil", err)
	}

	got, err := trustrule.UnverifiedIssuer(raw)
	if err != nil {
		t.Fatalf("UnverifiedIssuer = %v, want nil", err)
	}

	if got != valid {
		t.Errorf("UnverifiedIssuer = %q, want %q", got, valid)
	}
}

// TestVerifyRefusesALongTokenOfThreeParts checks the size limit alone: the
// token has three parts and a valid signature.
func TestVerifyRefusesALongTokenOfThreeParts(t *testing.T) {
	t.Parallel()

	s := edSigner(t, "guard-ed")

	c := claims()
	c["padding"] = strings.Repeat("a", trustrule.MaxTokenBytes)

	_, err := trustrule.Verify(s.token(t, s.header(), c), keys(t, s))
	if !errors.Is(err, trustrule.ErrToken) || !strings.Contains(err.Error(), "longer than") {
		t.Fatalf("Verify = %v, want the error of the size limit", err)
	}
}

// TestVerifyRefusesAnECSignatureOfAnotherLength checks the length of an EC
// signature alone: a zero byte in front of s keeps the value of s, so only
// the length check refuses the signature.
func TestVerifyRefusesAnECSignatureOfAnotherLength(t *testing.T) {
	t.Parallel()

	s := ecSigner(t, "ec")
	tok := s.token(t, s.header(), claims())

	if _, err := trustrule.Verify(tok, keys(t, s)); err != nil {
		t.Fatalf("Verify of the valid token = %v", err)
	}

	dot := strings.LastIndex(tok, ".")

	sig, err := base64.RawURLEncoding.DecodeString(tok[dot+1:])
	if err != nil {
		t.Fatalf("decode signature: %v", err)
	}

	long := append(append(append([]byte(nil), sig[:32]...), 0), sig[32:]...)

	if _, err := trustrule.Verify(tok[:dot+1]+b64(long), keys(t, s)); !errors.Is(err, trustrule.ErrToken) {
		t.Fatalf("Verify of a signature of %d bytes = %v, want ErrToken", len(long), err)
	}
}

// TestCheckTimeRefusesANegativeTime checks that a time claim before 1970 is
// refused. An nbf in the past would pass the time window, so only the check
// of the value refuses it.
func TestCheckTimeRefusesANegativeTime(t *testing.T) {
	t.Parallel()

	for _, claim := range []string{"nbf", "iat"} {
		c := claims()
		c[claim] = -1

		tok := verified(t, c)
		if err := tok.CheckTime(testTime()); !errors.Is(err, trustrule.ErrToken) {
			t.Errorf("%s -1: CheckTime = %v, want ErrToken", claim, err)
		}
	}
}

// TestMatchesHoldsItsOwnGuards checks the guards of Matches on rules that
// skipped Validate: a rule with no condition, a rule with no condition on
// the subject, and a claim that is null, which json.Unmarshal reads as the
// empty string.
func TestMatchesHoldsItsOwnGuards(t *testing.T) {
	t.Parallel()

	tok := trustrule.Token{
		Claims: map[string]json.RawMessage{
			"sub":        json.RawMessage(`"repo:acme/infra:ref:refs/heads/main"`),
			"repository": json.RawMessage(`"acme/infra"`),
			"ref":        json.RawMessage(`null`),
		},
		Alg: "EdDSA", Kid: "",
	}

	cases := map[string][]trustrule.Condition{
		"no condition":                nil,
		"no condition on the subject": {{Claim: "repository", Values: []string{"acme/infra"}}},
		"a null claim against an empty value": {
			{Claim: "ref", Values: []string{""}},
			{Claim: "sub", Values: []string{"repo:acme/infra:ref:refs/heads/main"}},
		},
	}

	for name, conds := range cases {
		r := trustrule.Rule{
			CreatedAt: testTime(), ID: trustrule.NewID(), Namespace: "acme", Name: "deploy",
			Issuer: "https://token.actions.example.com", Audience: "https://dustpan.example.com",
			Keys: trustrule.KeySourceDiscovery, StaticKeys: "", PrincipalID: "prn-worker", CreatedBy: "ada",
			Conditions: conds,
		}
		if r.Matches(&tok) {
			t.Errorf("%s: Matches = true, want false", name)
		}
	}
}

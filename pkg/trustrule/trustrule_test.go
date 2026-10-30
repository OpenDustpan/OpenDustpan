package trustrule_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/trustrule"
)

// validRule returns a rule that passes Validate.
func validRule() trustrule.Rule {
	return trustrule.Rule{
		CreatedAt: time.UnixMilli(1_700_000_000_000).UTC(), ID: trustrule.NewID(), Namespace: "acme",
		Name: "deploy", Issuer: "https://token.actions.example.com", Audience: "https://dustpan.example.com",
		Keys: trustrule.KeySourceDiscovery, StaticKeys: "", PrincipalID: "prn-worker", CreatedBy: "ada",
		Conditions: []trustrule.Condition{
			{Claim: "repository", Values: []string{"acme/infra"}},
			{Claim: "sub", Values: []string{"repo:acme/infra:ref:refs/heads/main"}},
		},
	}
}

func TestValidateAcceptsAValidRule(t *testing.T) {
	t.Parallel()

	r := validRule()
	if err := r.Validate(); err != nil {
		t.Fatalf("Validate = %v, want nil", err)
	}

	r.Keys, r.Issuer, r.StaticKeys = trustrule.KeySourceStatic, "ci-cluster-7", ed25519Set(t, "k1")
	if err := r.Validate(); err != nil {
		t.Fatalf("Validate of a static rule = %v, want nil", err)
	}
}

// TestValidateRefuses checks the boundary of the rule: each change gives
// ErrInvalid, as if every other check had failed.
func TestValidateRefuses(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		change func(r *trustrule.Rule)
	}{
		{"an ID of another kind", func(r *trustrule.Rule) { r.ID = "tok-abc" }},
		{"an empty ID", func(r *trustrule.Rule) { r.ID = "" }},
		{"an invalid namespace", func(r *trustrule.Rule) { r.Namespace = "Acme Corp" }},
		{"an empty name", func(r *trustrule.Rule) { r.Name = "" }},
		{"a name with a line break", func(r *trustrule.Rule) { r.Name = "a\nb" }},
		{"a long name", func(r *trustrule.Rule) { r.Name = strings.Repeat("a", trustrule.MaxNameLength+1) }},
		{"no creation time", func(r *trustrule.Rule) { r.CreatedAt = time.Time{} }},
		{"a principal ID with a slash", func(r *trustrule.Rule) { r.PrincipalID = "prn/../x" }},
		{"an empty audience", func(r *trustrule.Rule) { r.Audience = "" }},
		{"an audience with a format character", func(r *trustrule.Rule) { r.Audience = "a\u200bb" }},
		{"a name with a line separator", func(r *trustrule.Rule) { r.Name = "a\u2028b" }},
		{"an audience with a paragraph separator", func(r *trustrule.Rule) { r.Audience = "a\u2029b" }},
		{"an unknown key source", func(r *trustrule.Rule) { r.Keys = "jku" }},
		{"static keys with discovery", func(r *trustrule.Rule) { r.StaticKeys = "{}" }},
		{"a static rule with no keys", func(r *trustrule.Rule) { r.Keys = trustrule.KeySourceStatic }},
		{"no condition", func(r *trustrule.Rule) { r.Conditions = nil }},
		{"no condition on the subject", func(r *trustrule.Rule) { r.Conditions = r.Conditions[:1] }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := validRule()
			tc.change(&r)

			if err := r.Validate(); !errors.Is(err, trustrule.ErrInvalid) {
				t.Fatalf("Validate = %v, want ErrInvalid", err)
			}
		})
	}
}

// TestValidateDiscoveryIssuerRefusesAnUnsafeURL checks the form of the
// issuer that the server reads a discovery document from, before any
// connection opens. Defense in depth: the service checks the issuer against
// the egress rules, and the egress rules check the address of each
// connection again.
func TestValidateDiscoveryIssuerRefusesAnUnsafeURL(t *testing.T) {
	t.Parallel()

	for _, issuer := range []string{
		"http://token.actions.example.com",
		"https://user@token.actions.example.com",
		"https://token.actions.example.com?x=1",
		"https://token.actions.example.com#f",
		"https://user:pass@token.actions.example.com",
		"file:///etc/passwd",
		"https://",
		"token.actions.example.com",
		"",
	} {
		if err := trustrule.ValidateDiscoveryIssuer(issuer); !errors.Is(err, trustrule.ErrInvalid) {
			t.Errorf("ValidateDiscoveryIssuer(%q) = %v, want ErrInvalid", issuer, err)
		}
	}

	for _, issuer := range []string{
		"https://token.actions.githubusercontent.com", "https://gitlab.com", "https://oidc.circleci.com/org/1",
	} {
		if err := trustrule.ValidateDiscoveryIssuer(issuer); err != nil {
			t.Errorf("ValidateDiscoveryIssuer(%q) = %v, want nil", issuer, err)
		}
	}
}

// TestValidateConditions checks the rules of the conditions on the claims.
func TestValidateConditions(t *testing.T) {
	t.Parallel()

	sub := trustrule.Condition{Claim: "sub", Values: []string{"repo:acme/infra:ref:refs/heads/main"}}
	many := make([]string, trustrule.MaxValues+1)

	for i := range many {
		many[i] = strings.Repeat("v", i+1)
	}

	cases := []struct {
		name  string
		conds []trustrule.Condition
	}{
		{"none", nil},
		{"no subject", []trustrule.Condition{{Claim: "ref", Values: []string{"main"}}}},
		{"the issuer claim", []trustrule.Condition{{Claim: "iss", Values: []string{"x"}}, sub}},
		{"the audience claim", []trustrule.Condition{{Claim: "aud", Values: []string{"x"}}, sub}},
		{"the expiry claim", []trustrule.Condition{{Claim: "exp", Values: []string{"1"}}, sub}},
		{"the token ID claim", []trustrule.Condition{{Claim: "jti", Values: []string{"1"}}, sub}},
		{"a claim with a space", []trustrule.Condition{{Claim: "my claim", Values: []string{"x"}}, sub}},
		{"a claim twice", []trustrule.Condition{sub, sub}},
		{"out of order", []trustrule.Condition{sub, {Claim: "ref", Values: []string{"main"}}}},
		{"no value", []trustrule.Condition{{Claim: "sub", Values: nil}}},
		{"an empty value", []trustrule.Condition{{Claim: "sub", Values: []string{""}}}},
		{"a value twice", []trustrule.Condition{{Claim: "sub", Values: []string{"a", "a"}}}},
		{"a value with a line separator", []trustrule.Condition{{Claim: "sub", Values: []string{"a\u2028b"}}}},
		{"too many values", []trustrule.Condition{{Claim: "sub", Values: many}}},
		{"a value with a control character", []trustrule.Condition{{Claim: "sub", Values: []string{"a\x00b"}}}},
		{"a long value", []trustrule.Condition{
			{Claim: "sub", Values: []string{strings.Repeat("a", trustrule.MaxValueLength+1)}},
		}},
	}

	for _, tc := range cases {
		if err := trustrule.ValidateConditions(tc.conds); !errors.Is(err, trustrule.ErrInvalid) {
			t.Errorf("%s: ValidateConditions = %v, want ErrInvalid", tc.name, err)
		}
	}

	// A wildcard is only a character: the rule takes it, and it matches only
	// itself.
	err := trustrule.ValidateConditions([]trustrule.Condition{{Claim: "sub", Values: []string{"repo:*"}}})
	if err != nil {
		t.Errorf("a value with a star = %v, want nil", err)
	}
}

func TestSortConditionsSortsByClaimAndCopies(t *testing.T) {
	t.Parallel()

	in := []trustrule.Condition{
		{Claim: "sub", Values: []string{"b", "a"}},
		{Claim: "repository", Values: []string{"acme/infra"}},
	}

	out := trustrule.SortConditions(in)
	if out[0].Claim != "repository" || out[1].Claim != "sub" || out[1].Values[0] != "b" {
		t.Fatalf("SortConditions = %+v", out)
	}

	out[1].Values[0] = "changed"
	if in[0].Values[0] != "b" {
		t.Fatal("SortConditions shares the values with its input")
	}
}

func TestRefNamesOnlyARuleID(t *testing.T) {
	t.Parallel()

	id := trustrule.NewID()
	if got := trustrule.Ref(id); got != "trust-rule:"+string(id) {
		t.Errorf("Ref(%s) = %q", id, got)
	}

	if got := trustrule.Ref("trr-a\nb"); got != "trust-rule:invalid" {
		t.Errorf("Ref of an invalid ID = %q, want trust-rule:invalid", got)
	}
}

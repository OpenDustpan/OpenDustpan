package errcode

// The codes of the workload trust rules. A code is never removed and never
// changes its meaning.
const (
	// TrustRuleAmbiguous means that more than one trust rule accepts the
	// token of an exchange, and the request names no rule.
	TrustRuleAmbiguous = "trust-rule.ambiguous"
	// TrustRuleNoMatch means that no trust rule of the namespace accepts the
	// token of an exchange.
	TrustRuleNoMatch = "trust-rule.no-match"
	// TrustRuleAlreadyExists means that the namespace already has a trust rule
	// with the name.
	TrustRuleAlreadyExists = "trust-rule.already-exists"
	// TrustRuleInvalid means that the trust rule is not valid.
	TrustRuleInvalid = "trust-rule.invalid"
	// TrustRuleIssuerRefused means that the issuer of the trust rule is an
	// issuer of this installation.
	TrustRuleIssuerRefused = "trust-rule.issuer-refused"
	// TrustRuleLimit means that the namespace already has the largest number
	// of trust rules.
	TrustRuleLimit = "trust-rule.limit"
	// TrustRuleNotFound means that the namespace has no trust rule with the
	// ID.
	TrustRuleNotFound = "trust-rule.not-found"
	// TrustRulePrincipalRefused means that the principal cannot be the
	// principal of a trust rule of the namespace.
	TrustRulePrincipalRefused = "trust-rule.principal-refused"
	// TrustRuleSignInRequired means that an access token cannot create a
	// trust rule, and the caller must sign in.
	TrustRuleSignInRequired = "trust-rule.sign-in-required"
)

// trustRuleSpecs are the codes of the workload trust rules. No code of the
// area has arguments.
func trustRuleSpecs() []Spec {
	meanings := [][2]string{
		{
			TrustRuleAmbiguous,
			"more than one trust rule accepts the token of an exchange, and the request names no rule",
		},
		{TrustRuleNoMatch, "no trust rule of the namespace accepts the token of an exchange"},
		{TrustRuleAlreadyExists, "the namespace already has a trust rule with the name"},
		{TrustRuleInvalid, "the trust rule is not valid"},
		{TrustRuleIssuerRefused, "the issuer of the trust rule is an issuer of this installation"},
		{TrustRuleLimit, "the namespace already has the largest number of trust rules"},
		{TrustRuleNotFound, "the namespace has no trust rule with the ID"},
		{TrustRulePrincipalRefused, "the principal cannot be the principal of a trust rule of the namespace"},
		{TrustRuleSignInRequired, "an access token cannot create a trust rule, and the caller must sign in"},
	}

	out := make([]Spec, 0, len(meanings))
	for _, m := range meanings {
		out = append(out, Spec{Code: m[0], Meaning: m[1], Args: nil})
	}

	return out
}

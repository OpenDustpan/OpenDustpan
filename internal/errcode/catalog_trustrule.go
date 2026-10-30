package errcode

// The codes of the workload trust rules. A code is never removed and never
// changes its meaning.
const (
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

// trustRuleSpecs are the codes of the workload trust rules.
func trustRuleSpecs() []Spec {
	return []Spec{
		{
			Code:    TrustRuleAlreadyExists,
			Meaning: "the namespace already has a trust rule with the name",
			Args:    nil,
		},
		{Code: TrustRuleInvalid, Meaning: "the trust rule is not valid", Args: nil},
		{
			Code:    TrustRuleIssuerRefused,
			Meaning: "the issuer of the trust rule is an issuer of this installation",
			Args:    nil,
		},
		{
			Code:    TrustRuleLimit,
			Meaning: "the namespace already has the largest number of trust rules",
			Args:    nil,
		},
		{Code: TrustRuleNotFound, Meaning: "the namespace has no trust rule with the ID", Args: nil},
		{
			Code:    TrustRulePrincipalRefused,
			Meaning: "the principal cannot be the principal of a trust rule of the namespace",
			Args:    nil,
		},
		{
			Code:    TrustRuleSignInRequired,
			Meaning: "an access token cannot create a trust rule, and the caller must sign in",
			Args:    nil,
		},
	}
}

package errcode

// The codes of the area. A code is never removed and never changes its
// meaning.
const (
	// MTLSAmbiguousRule means that more than one trust rule matches the client certificate.
	MTLSAmbiguousRule = "mtls.ambiguous-rule"
	// MTLSBundleDisabled means that the trust bundle of the rule does not exist or has no active version.
	MTLSBundleDisabled = "mtls.bundle-disabled"
	// MTLSExpired means that a certificate of the client chain ended.
	MTLSExpired = "mtls.expired"
	// MTLSNoClientCertificate means that the TLS handshake gave no client certificate.
	MTLSNoClientCertificate = "mtls.no-client-certificate"
	// MTLSNoMatchingRule means that no trust rule of the namespace matches the client certificate.
	MTLSNoMatchingRule = "mtls.no-matching-rule"
	// MTLSNotYetValid means that a certificate of the client chain is not valid yet.
	MTLSNotYetValid = "mtls.not-yet-valid"
	// MTLSPrincipalDisabled means that the principal of the rule cannot get a token.
	MTLSPrincipalDisabled = "mtls.principal-disabled"
	// MTLSRateLimited means that the caller sent too many exchange requests.
	MTLSRateLimited = "mtls.rate-limited"
	// MTLSRevocationUnknown means that the server could not check if the client certificate is revoked.
	MTLSRevocationUnknown = "mtls.revocation-unknown"
	// MTLSRevoked means that the issuer revoked the client certificate.
	MTLSRevoked = "mtls.revoked"
	// MTLSUnknownIssuer means that no trust bundle of the namespace issued the client chain.
	MTLSUnknownIssuer = "mtls.unknown-issuer"
)

// mtlsSpecs are the codes of the mTLS token exchange. The answer status of
// each code is 401, except mtls.rate-limited, which is 429.
func mtlsSpecs() []Spec {
	meanings := []struct{ code, meaning string }{
		{MTLSAmbiguousRule, "more than one trust rule matches the client certificate"},
		{MTLSBundleDisabled, "the trust bundle of the rule does not exist or has no active version"},
		{MTLSExpired, "a certificate of the client chain ended"},
		{MTLSNoClientCertificate, "the TLS handshake gave no client certificate"},
		{MTLSNoMatchingRule, "no trust rule of the namespace matches the client certificate"},
		{MTLSNotYetValid, "a certificate of the client chain is not valid yet"},
		{MTLSPrincipalDisabled, "the principal of the rule cannot get a token"},
		{MTLSRateLimited, "the caller sent too many exchange requests"},
		{MTLSRevocationUnknown, "the server could not check if the client certificate is revoked"},
		{MTLSRevoked, "the issuer revoked the client certificate"},
		{MTLSUnknownIssuer, "no trust bundle of the namespace issued the client chain"},
	}

	specs := make([]Spec, 0, len(meanings))
	for _, m := range meanings {
		specs = append(specs, Spec{Code: m.code, Meaning: m.meaning, Args: nil})
	}

	return specs
}

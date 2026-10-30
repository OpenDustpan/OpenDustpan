package errcode

// The codes of the issuer. A code is never removed and never changes its
// meaning.
const (
	// IssuerConflict means that the issuer of the namespace changed, or has
	// keys, so the request cannot run.
	IssuerConflict = "issuer.conflict"
	// IssuerInvalid means that a setting or a request of the issuer is not
	// valid.
	IssuerInvalid = "issuer.invalid"
	// IssuerKeyExpired means that the active key of an issuer with an issuerURL
	// signed for 180 days.
	IssuerKeyExpired = "issuer.key-expired"
	// IssuerKeyMissing means that the issuer has no signing key that a copied
	// JWKS holds.
	IssuerKeyMissing = "issuer.key-missing"
	// IssuerNotConfigured means that the server has no issuer host.
	IssuerNotConfigured = "issuer.not-configured"
)

// issuerSpecs are the codes of the issuer.
func issuerSpecs() []Spec {
	return []Spec{
		{Code: IssuerConflict, Meaning: "the issuer of the namespace changed or has keys, so the request cannot run", Args: nil},
		{Code: IssuerInvalid, Meaning: "a setting or a request of the issuer is not valid", Args: nil},
		{
			Code:    IssuerKeyExpired,
			Meaning: "the active key of an issuer with an issuerURL signed for 180 days",
			Args:    nil,
		},
		{
			Code:    IssuerKeyMissing,
			Meaning: "the issuer has no signing key that a copied JWKS holds",
			Args:    nil,
		},
		{Code: IssuerNotConfigured, Meaning: "the server has no issuer host", Args: nil},
	}
}

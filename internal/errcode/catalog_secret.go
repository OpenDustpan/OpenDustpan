package errcode

// SecretKeyNotConfigured is a request that needs the encryption key of the
// installation when the operator has not configured one.
const SecretKeyNotConfigured = "secret.key-not-configured"

// secretSpecs are the codes of the secrets and their keys.
func secretSpecs() []Spec {
	return []Spec{
		{
			Code:    SecretKeyNotConfigured,
			Meaning: "the operator has not configured the encryption key that the request needs",
			Args:    nil,
		},
	}
}

package errcode

// The codes of the secrets and their keys. A code is never removed and never
// changes its meaning.
const (
	// SecretConsoleSession is a console session on a route that encrypts or
	// decrypts the secrets of a stack.
	SecretConsoleSession = "secret.console-session"
	// SecretKeyNotConfigured is a request that needs the encryption key of the
	// installation when the operator has not configured one.
	SecretKeyNotConfigured = "secret.key-not-configured"
	// SecretNotDecrypted is a value that does not decrypt.
	SecretNotDecrypted = "secret.not-decrypted"
	// SecretTooLarge is a value or a batch of values over its limit.
	SecretTooLarge = "secret.too-large"
)

// secretSpecs are the codes of the secrets and their keys.
func secretSpecs() []Spec {
	return []Spec{
		{
			Code:    SecretConsoleSession,
			Meaning: "a console session cannot encrypt or decrypt secrets: the request needs an access token",
			Args:    nil,
		},
		{
			Code:    SecretKeyNotConfigured,
			Meaning: "the operator has not configured the encryption key that the request needs",
			Args:    nil,
		},
		{
			Code:    SecretNotDecrypted,
			Meaning: "the value does not decrypt: it belongs to another stack, or the key changed",
			Args:    nil,
		},
		{Code: SecretTooLarge, Meaning: "a value or a batch of values is larger than its limit", Args: nil},
	}
}

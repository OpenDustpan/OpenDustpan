package errcode

// The codes of the secrets and their keys. A code is never removed and never
// changes its meaning.
const (
	// SecretConsoleSession is a console session on a route that encrypts or
	// decrypts the secrets of a stack.
	SecretConsoleSession = "secret.console-session"
	// SecretItemKeyBlobClockSkew is a listing of item key blobs whose time is
	// too far ahead of the clock of the server.
	SecretItemKeyBlobClockSkew = "secret.item-key-blob-clock-skew"
	// SecretItemKeyAttemptExpired is a write of an item key that did not
	// commit before its attempt deadline.
	SecretItemKeyAttemptExpired = "secret.item-key-attempt-expired"
	// SecretItemKeyBlobMismatch is a wrapped item key blob whose size or MD5
	// is not the size or the MD5 that its record names.
	SecretItemKeyBlobMismatch = "secret.item-key-blob-mismatch"
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
			Code: SecretItemKeyBlobClockSkew,
			Meaning: "the clock of the blob store is ahead of the clock of the server by more than the reaper allows, " +
				"so the reaper deletes no key blob",
			Args: nil,
		},
		{
			Code:    SecretItemKeyAttemptExpired,
			Meaning: "the write of the key of an item did not commit before its attempt deadline, so it rolled back",
			Args:    nil,
		},
		{
			Code: SecretItemKeyBlobMismatch,
			Meaning: "the stored key of an item does not match its record: the blob store replaced or " +
				"changed the object, so the server does not use the key",
			Args: nil,
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

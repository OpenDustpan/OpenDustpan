package pagetoken

// The guards of Decode, so the external tests send each hostile form to one
// check alone. A token is accepted only when every guard accepts it.

// HasCanonicalAlphabet reports whether token holds only the unpadded
// base64url alphabet.
func HasCanonicalAlphabet(token string) bool { return isCanonicalBase64URL(token) }

// DecodeStrict returns the strict base64url decode of token.
func DecodeStrict(token string) ([]byte, error) { return strictDecode(token) }

// AcceptsCanonicalText reports whether raw encodes to token.
func AcceptsCanonicalText(raw []byte, token string) bool { return isCanonicalText(raw, token) }

package sshsig

// CheckECDSA calls checkECDSA alone. Verify calls checkECDSA first, so a
// black-box test cannot show that this layer stops a key by itself.
func CheckECDSA(k PublicKey) error { return k.checkECDSA() }

// VerifyECDSA calls verifyECDSA alone, with none of the checks that Verify
// makes before it, so that a test sends hostile input to the layers inside it.
func VerifyECDSA(s Signature, key PublicKey, signed []byte) (Result, error) {
	return s.verifyECDSA(key, signed)
}

// VerifyKey calls verifyKey alone, below the key check, the key match, the
// namespace check, the hash check and the signature type check of Verify.
func VerifyKey(s Signature, key PublicKey, namespace string, digest []byte) (Result, error) {
	return s.verifyKey(key, namespace, digest)
}

package sshsig

import "crypto/ecdsa"

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

// ParseECDSAFields calls parseECDSAKey alone, on the fields after the key type:
// the curve name and the point. ParsePublicKey checks the key after the
// parser, so only a direct call shows that the parser refuses a point itself.
func ParseECDSAFields(fields []byte) (PublicKey, error) {
	r := reader{b: fields, err: nil}

	return parseECDSAKey(&r)
}

// EncodeECDSAPoint calls the one encoder of the ECDSA point alone.
func EncodeECDSAPoint(pub *ecdsa.PublicKey) ([]byte, error) { return ecdsaPoint(pub) }

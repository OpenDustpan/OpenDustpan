// Package sshsig parses and verifies signatures in the format that
// "ssh-keygen -Y sign" writes (the SSHSIG format of OpenSSH). It accepts the
// key types ssh-ed25519, sk-ssh-ed25519@openssh.com and ecdsa-sha2-nistp256,
// and the hash algorithms sha256 and sha512. It refuses each other value.
//
// A signature holds the public key of the signer. Verify compares that key
// with the key that the caller gives and verifies with the caller's key
// only.
package sshsig

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/asn1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"math/big"
	"strings"
)

// The namespaces of the step-up signatures. A signature for git or for a
// file has another namespace, so it never verifies as a step-up signature.
const (
	// NamespaceStepUp is the namespace of a step-up signature for a write
	// or a window.
	NamespaceStepUp = "step-up@opendustpan.org"
	// NamespaceKeyAdd is the namespace of the proof of possession of a new
	// step-up key.
	NamespaceKeyAdd = "step-up-key-add@opendustpan.org"
)

// The key types that the package accepts.
const (
	TypeEd25519   = "ssh-ed25519"
	TypeSKEd25519 = "sk-ssh-ed25519@openssh.com"
	// TypeECDSAP256 is the key type of an ECDSA key on the curve P-256.
	TypeECDSAP256 = "ecdsa-sha2-nistp256"
)

// The hash algorithms that the package accepts.
const (
	HashSHA256 = "sha256"
	HashSHA512 = "sha512"
)

// The flags of a security key signature.
const (
	// FlagUserPresent is the user presence flag.
	FlagUserPresent byte = 0x01
	// FlagUserVerified is the user verification flag.
	FlagUserVerified byte = 0x04
)

// Limits of the parser. A value above a limit is malformed.
const (
	// MaxArmoredSize is the maximum size of an armored signature in bytes.
	MaxArmoredSize = 4096
	// MaxNamespaceSize is the maximum size of a namespace in bytes.
	MaxNamespaceSize = 64
	// MaxApplicationSize is the maximum size of the application of a
	// security key in bytes.
	MaxApplicationSize = 256
	maxPublicKeySize   = 512
	maxHashNameSize    = 16
	maxSignatureSize   = 512
	maxTypeSize        = 64
	maxArmorLine       = 76
	ed25519KeySize     = ed25519.PublicKeySize
	ed25519SigSize     = ed25519.SignatureSize
	// ecdsaCurveName is the curve name of TypeECDSAP256 on the wire.
	ecdsaCurveName = "nistp256"
	// ecdsaPointSize is the size of an uncompressed P-256 point: 0x04, X
	// and Y.
	ecdsaPointSize = 65
	// maxMPIntSize is the size of an mpint of a P-256 value: 32 bytes and
	// a 0x00 prefix.
	maxMPIntSize = 33
	// ecdsaSigBlobSize is the size of the largest signature blob: the
	// mpints r and s, each with its 4-byte length field and at most
	// maxMPIntSize bytes.
	ecdsaSigBlobSize  = 74
	magic             = "SSHSIG"
	version           = 1
	applicationPrefix = "ssh:"
	armorBegin        = "-----BEGIN SSH SIGNATURE-----"
	armorEnd          = "-----END SSH SIGNATURE-----"
)

// Errors of the package.
var (
	// ErrMalformed reports bytes that are not a signature or a public key
	// in the accepted format.
	ErrMalformed = errors.New("malformed SSH signature or key")
	// ErrVersion reports a signature version other than 1.
	ErrVersion = errors.New("SSH signature version not supported")
	// ErrKeyType reports a key type or a signature type outside the
	// allowlist.
	ErrKeyType = errors.New("SSH key type not allowed")
	// ErrNamespace reports a namespace that is not the expected namespace.
	ErrNamespace = errors.New("SSH signature namespace does not match")
	// ErrHashAlgorithm reports a hash algorithm other than sha256 or sha512.
	ErrHashAlgorithm = errors.New("SSH signature hash algorithm not allowed")
	// ErrKeyMismatch reports a signature whose public key is not the
	// expected key.
	ErrKeyMismatch = errors.New("SSH signature key is not the expected key")
	// ErrFlags reports a security key signature with a flag other than user
	// presence and user verification.
	ErrFlags = errors.New("security key signature flags not allowed")
	// ErrUserPresence reports a security key signature without user
	// presence.
	ErrUserPresence = errors.New("security key signature has no user presence")
	// ErrVerify reports a signature that does not verify with the key.
	ErrVerify = errors.New("SSH signature does not verify")
)

// PublicKey is a public key of an accepted type.
type PublicKey struct {
	// Type is TypeEd25519, TypeSKEd25519 or TypeECDSAP256.
	Type string
	// Key is the Ed25519 public key. It is empty for TypeECDSAP256.
	Key ed25519.PublicKey
	// Application is the application of a security key, for example
	// "ssh:". It is empty for the other types.
	Application string
	// ECDSA is the P-256 public key of TypeECDSAP256. It is nil for the
	// other types.
	ECDSA *ecdsa.PublicKey
}

// Signature is a parsed signature.
type Signature struct {
	// PublicKey is the public key in the signature, in its wire form.
	PublicKey []byte
	// Namespace is the namespace that the signer gave.
	Namespace string
	// HashAlgorithm is HashSHA256 or HashSHA512.
	HashAlgorithm string
	// Type is the signature type: the key type of the signer.
	Type string
	// Blob is the signature. For an Ed25519 type it is the 64-byte Ed25519
	// signature. For TypeECDSAP256 it is the mpint r followed by the mpint s.
	Blob []byte
	// Flags and Counter are the flags and the signature counter of a
	// security key signature. Both are 0 unless Type is TypeSKEd25519.
	Flags   byte
	Counter uint32
}

// Result is what a verified signature says about the key.
type Result struct {
	// UserPresent and UserVerified are the flags of a security key
	// signature. Both are false for a key that is not a security key. A
	// software middleware can set them, so they prove no hardware.
	UserPresent  bool
	UserVerified bool
	// Counter is the signature counter of a security key signature.
	Counter uint32
}

// ParsePublicKey parses a public key in its SSH wire form. It returns
// ErrKeyType for a type outside the allowlist and ErrMalformed for each
// other fault.
func ParsePublicKey(wire []byte) (PublicKey, error) {
	if len(wire) > maxPublicKeySize {
		return PublicKey{}, fmt.Errorf("%w: public key of %d bytes", ErrMalformed, len(wire))
	}

	r := reader{b: wire, err: nil}
	keyType := string(r.str(maxTypeSize))

	var (
		pk  PublicKey
		err error
	)

	switch keyType {
	case TypeEd25519, TypeSKEd25519:
		pk, err = parseEd25519Key(&r, keyType)
	case TypeECDSAP256:
		pk, err = parseECDSAKey(&r)
	default:
		if r.err != nil {
			return PublicKey{}, r.err
		}

		return PublicKey{}, fmt.Errorf("%w: %q", ErrKeyType, printable(keyType))
	}

	if err != nil {
		return PublicKey{}, err
	}

	if err := pk.check(); err != nil {
		return PublicKey{}, err
	}

	return pk, nil
}

// parseEd25519Key reads the fields of an Ed25519 key or a security key after
// the type. It does not check the key; ParsePublicKey does.
func parseEd25519Key(r *reader, keyType string) (PublicKey, error) {
	key := r.str(ed25519KeySize)

	var app string
	if keyType == TypeSKEd25519 {
		app = string(r.str(MaxApplicationSize))
	}

	if err := r.end(); err != nil {
		return PublicKey{}, err
	}

	if len(key) != ed25519KeySize {
		return PublicKey{}, fmt.Errorf("%w: Ed25519 key of %d bytes", ErrMalformed, len(key))
	}

	return PublicKey{Type: keyType, Key: ed25519.PublicKey(bytes.Clone(key)), Application: app, ECDSA: nil}, nil
}

// parseECDSAKey reads the curve name and the point of a P-256 key after the
// type. It returns the key with its point parsed.
func parseECDSAKey(r *reader) (PublicKey, error) {
	curve := r.str(maxTypeSize)
	point := r.str(ecdsaPointSize)

	if err := r.end(); err != nil {
		return PublicKey{}, err
	}

	if string(curve) != ecdsaCurveName {
		return PublicKey{}, fmt.Errorf("%w: ECDSA curve %q", ErrMalformed, printable(string(curve)))
	}

	ec, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), point)
	if err != nil {
		return PublicKey{}, fmt.Errorf("%w: ECDSA point", ErrMalformed)
	}

	return PublicKey{Type: TypeECDSAP256, Key: nil, Application: "", ECDSA: ec}, nil
}

// Marshal returns the key in its SSH wire form. For a key that check
// refuses, it returns the type alone. A valid key always has more fields,
// so that result never equals the wire of a key that parses.
func (k PublicKey) Marshal() []byte {
	b := appendString(nil, []byte(k.Type))

	switch k.Type {
	case TypeEd25519:
		return appendString(b, k.Key)
	case TypeSKEd25519:
		b = appendString(b, k.Key)
		return appendString(b, []byte(k.Application))
	case TypeECDSAP256:
		if k.ECDSA == nil {
			return b
		}

		point, err := k.ECDSA.Bytes()
		if err != nil {
			return b
		}

		b = appendString(b, []byte(ecdsaCurveName))

		return appendString(b, point)
	default:
		return b
	}
}

// check checks the fields of a key.
func (k PublicKey) check() error {
	switch k.Type {
	case TypeEd25519, TypeSKEd25519:
		return k.checkEd25519()
	case TypeECDSAP256:
		return k.checkECDSA()
	default:
		return fmt.Errorf("%w: %q", ErrKeyType, printable(k.Type))
	}
}

// checkEd25519 checks an Ed25519 key or a security key.
func (k PublicKey) checkEd25519() error {
	if len(k.Key) != ed25519KeySize {
		return fmt.Errorf("%w: Ed25519 key of %d bytes", ErrMalformed, len(k.Key))
	}

	if k.ECDSA != nil {
		return fmt.Errorf("%w: ECDSA point on an Ed25519 key", ErrMalformed)
	}

	if k.Type == TypeEd25519 {
		if k.Application != "" {
			return fmt.Errorf("%w: application on a key that is not a security key", ErrMalformed)
		}

		return nil
	}

	// ssh-keygen only enrolls an application that starts with "ssh:".
	// Other applications are web origins, and the package refuses them.
	if !strings.HasPrefix(k.Application, applicationPrefix) || !isToken(k.Application, MaxApplicationSize) {
		return fmt.Errorf("%w: application %q", ErrMalformed, printable(k.Application))
	}

	return nil
}

// checkECDSA checks a P-256 key.
func (k PublicKey) checkECDSA() error {
	if k.ECDSA == nil || k.ECDSA.Curve != elliptic.P256() {
		return fmt.Errorf("%w: ECDSA key that is not on P-256", ErrMalformed)
	}

	// Defense in depth: parseECDSAKey parses the point with a check of the
	// curve too, and VerifyASN1 refuses a point that is not on the curve; this
	// check stays so that a key that a caller builds by hand, with no
	// coordinates, with the point at infinity or with a point off the curve,
	// never reaches Marshal or the verifier.
	if !pointOnCurve(k.ECDSA) {
		return fmt.Errorf("%w: ECDSA point is not on the curve", ErrMalformed)
	}

	if len(k.Key) != 0 || k.Application != "" {
		return fmt.Errorf("%w: Ed25519 fields on an ECDSA key", ErrMalformed)
	}

	return nil
}

// pointOnCurve reports whether pub holds a point of P-256 that is not the
// point at infinity. Bytes encodes only such a point. A key without
// coordinates makes Bytes panic, and the function fails closed on it: it
// returns false.
func pointOnCurve(pub *ecdsa.PublicKey) bool {
	ok := false

	func() {
		defer func() {
			if recover() != nil {
				ok = false
			}
		}()

		_, err := pub.Bytes()
		ok = err == nil
	}()

	return ok
}

// Parse parses an armored signature, exactly as "ssh-keygen -Y sign"
// writes it. Lines of base64 can have up to 76 characters. It
// returns ErrMalformed, ErrVersion, ErrKeyType or ErrHashAlgorithm.
func Parse(armored []byte) (Signature, error) {
	blob, err := unarmor(armored)
	if err != nil {
		return Signature{}, err
	}

	return ParseBlob(blob)
}

// unarmor returns the bytes of an armored signature. It accepts only the
// form that ssh-keygen writes: the begin line, lines of base64, and the end
// line, each line followed by LF.
func unarmor(armored []byte) ([]byte, error) {
	if len(armored) > MaxArmoredSize {
		return nil, fmt.Errorf("%w: armored signature of %d bytes", ErrMalformed, len(armored))
	}

	text, ok := strings.CutSuffix(string(armored), "\n")
	if !ok {
		return nil, fmt.Errorf("%w: no LF after the armor", ErrMalformed)
	}

	lines := strings.Split(text, "\n")

	const minLines = 3
	if len(lines) < minLines || lines[0] != armorBegin || lines[len(lines)-1] != armorEnd {
		return nil, fmt.Errorf("%w: no SSH signature armor", ErrMalformed)
	}

	var body strings.Builder

	for _, line := range lines[1 : len(lines)-1] {
		if line == "" || len(line) > maxArmorLine || !isBase64(line) {
			return nil, fmt.Errorf("%w: armor line is not base64", ErrMalformed)
		}

		body.WriteString(line)
	}

	blob, err := base64.StdEncoding.Strict().DecodeString(body.String())
	if err != nil {
		return nil, fmt.Errorf("%w: armor is not base64", ErrMalformed)
	}

	return blob, nil
}

// ParseBlob parses the bytes of a signature without the armor.
func ParseBlob(blob []byte) (Signature, error) {
	if len(blob) > MaxArmoredSize {
		return Signature{}, fmt.Errorf("%w: signature of %d bytes", ErrMalformed, len(blob))
	}

	r := reader{b: blob, err: nil}

	if m := r.take(len(magic)); r.err == nil && string(m) != magic {
		return Signature{}, fmt.Errorf("%w: no SSHSIG magic", ErrMalformed)
	}

	if v := r.uint32(); r.err == nil && v != version {
		return Signature{}, fmt.Errorf("%w: version %d", ErrVersion, v)
	}

	publicKey := bytes.Clone(r.str(maxPublicKeySize))
	namespace := string(r.str(MaxNamespaceSize))
	reserved := r.str(maxSignatureSize)
	hashAlgorithm := string(r.str(maxHashNameSize))
	inner := r.str(maxSignatureSize)

	if err := r.end(); err != nil {
		return Signature{}, err
	}

	if len(reserved) != 0 {
		return Signature{}, fmt.Errorf("%w: reserved field is not empty", ErrMalformed)
	}

	if !isToken(namespace, MaxNamespaceSize) {
		return Signature{}, fmt.Errorf("%w: namespace %q", ErrMalformed, printable(namespace))
	}

	if !allowedHash(hashAlgorithm) {
		return Signature{}, fmt.Errorf("%w: %q", ErrHashAlgorithm, printable(hashAlgorithm))
	}

	sig, err := parseInner(inner)
	if err != nil {
		return Signature{}, err
	}

	sig.PublicKey = publicKey
	sig.Namespace = namespace
	sig.HashAlgorithm = hashAlgorithm

	return sig, nil
}

// parseInner parses the signature field: the signature type, the signature
// blob, and for a security key the flags and the counter. It returns a
// Signature with these fields only.
func parseInner(inner []byte) (Signature, error) {
	r := reader{b: inner, err: nil}
	sigType := string(r.str(maxTypeSize))
	blob := bytes.Clone(r.str(ecdsaSigBlobSize))

	var (
		flags   byte
		counter uint32
	)

	switch sigType {
	case TypeEd25519, TypeECDSAP256:
	case TypeSKEd25519:
		flags = r.byte()
		counter = r.uint32()
	default:
		if r.err != nil {
			return Signature{}, r.err
		}

		return Signature{}, fmt.Errorf("%w: signature type %q", ErrKeyType, printable(sigType))
	}

	if err := r.end(); err != nil {
		return Signature{}, err
	}

	// The Ed25519 types have a fixed blob. The ECDSA blob has two mpints, so
	// its length is checked when it is verified.
	if sigType != TypeECDSAP256 && len(blob) != ed25519SigSize {
		return Signature{}, fmt.Errorf("%w: Ed25519 signature of %d bytes", ErrMalformed, len(blob))
	}

	return Signature{
		PublicKey:     nil,
		Namespace:     "",
		HashAlgorithm: "",
		Type:          sigType,
		Blob:          blob,
		Flags:         flags,
		Counter:       counter,
	}, nil
}

// Verify checks that s is a signature of key over message in the namespace
// namespace, and returns its result. It verifies with key only, never with
// the key in s. It returns ErrKeyMismatch, ErrNamespace, ErrHashAlgorithm,
// ErrKeyType, ErrMalformed, ErrFlags, ErrUserPresence or ErrVerify.
func (s Signature) Verify(key PublicKey, namespace string, message []byte) (Result, error) {
	// Defense in depth: ParsePublicKey checks the key too; this check stays
	// so that a key that a caller builds cannot skip it.
	if err := key.check(); err != nil {
		return Result{}, err
	}

	if !bytes.Equal(s.PublicKey, key.Marshal()) {
		return Result{}, ErrKeyMismatch
	}

	// Defense in depth: Parse refuses an empty namespace too; this check
	// stays so that an empty expected namespace never matches.
	if !isToken(namespace, MaxNamespaceSize) || s.Namespace != namespace {
		return Result{}, fmt.Errorf("%w: got %q", ErrNamespace, printable(s.Namespace))
	}

	// Defense in depth: Parse refuses another hash algorithm too; this check
	// stays so that a signature that a caller builds cannot skip it.
	digest, err := hashMessage(s.HashAlgorithm, message)
	if err != nil {
		return Result{}, err
	}

	if s.Type != key.Type {
		return Result{}, fmt.Errorf("%w: signature type %q for key type %q",
			ErrKeyType, printable(s.Type), key.Type)
	}

	return s.verifyKey(key, namespace, digest)
}

// verifyKey verifies the signed data of the signature with key, by the rules
// of the key type. digest is the hash of the message.
func (s Signature) verifyKey(key PublicKey, namespace string, digest []byte) (Result, error) {
	signed := signedData(namespace, s.HashAlgorithm, digest)

	switch key.Type {
	case TypeEd25519:
		return s.verifyEd25519(key, signed)
	case TypeSKEd25519:
		return s.verifySK(key, signed)
	case TypeECDSAP256:
		// Defense in depth: parseInner never sets flags or a counter for this
		// type, and Verify does not rely on it; this check stays so that a
		// Signature that a caller builds cannot verify with them.
		if s.Flags != 0 || s.Counter != 0 {
			return Result{}, fmt.Errorf("%w: flags on a key that is not a security key", ErrFlags)
		}

		return s.verifyECDSA(key, signed)
	default:
		// Defense in depth: checkKey refuses another type too; this case
		// stays so that a new type cannot verify before it has its own branch.
		return Result{}, fmt.Errorf("%w: %q", ErrKeyType, printable(key.Type))
	}
}

// verifyEd25519 verifies an Ed25519 signature. It refuses flags and a
// counter, which only a security key has.
func (s Signature) verifyEd25519(key PublicKey, signed []byte) (Result, error) {
	if len(s.Blob) != ed25519SigSize {
		return Result{}, fmt.Errorf("%w: Ed25519 signature of %d bytes", ErrMalformed, len(s.Blob))
	}

	if s.Flags != 0 || s.Counter != 0 {
		return Result{}, fmt.Errorf("%w: flags on a key that is not a security key", ErrFlags)
	}

	if !ed25519.Verify(key.Key, signed, s.Blob) {
		return Result{}, ErrVerify
	}

	return Result{UserPresent: false, UserVerified: false, Counter: 0}, nil
}

// verifySK verifies a security key signature. The security key signs the
// SHA-256 of the application, the flags, the counter and the SHA-256 of the
// signed data.
func (s Signature) verifySK(key PublicKey, signed []byte) (Result, error) {
	if len(s.Blob) != ed25519SigSize {
		return Result{}, fmt.Errorf("%w: Ed25519 signature of %d bytes", ErrMalformed, len(s.Blob))
	}

	if s.Flags&^(FlagUserPresent|FlagUserVerified) != 0 {
		return Result{}, fmt.Errorf("%w: flags 0x%02x", ErrFlags, s.Flags)
	}

	if s.Flags&FlagUserPresent == 0 {
		return Result{}, ErrUserPresence
	}

	appHash := sha256.Sum256([]byte(key.Application))
	dataHash := sha256.Sum256(signed)

	const skSignedSize = sha256.Size + 1 + 4 + sha256.Size

	tbs := make([]byte, 0, skSignedSize)
	tbs = append(tbs, appHash[:]...)
	tbs = append(tbs, s.Flags)
	tbs = binary.BigEndian.AppendUint32(tbs, s.Counter)
	tbs = append(tbs, dataHash[:]...)

	if !ed25519.Verify(key.Key, tbs, s.Blob) {
		return Result{}, ErrVerify
	}

	return Result{
		UserPresent:  true,
		UserVerified: s.Flags&FlagUserVerified != 0,
		Counter:      s.Counter,
	}, nil
}

// verifyECDSA verifies an ECDSA signature. The blob holds the mpint r and
// the mpint s. The key signs the SHA-256 of the signed data.
func (s Signature) verifyECDSA(key PublicKey, signed []byte) (Result, error) {
	// Defense in depth: check refuses a key without a point; this check stays
	// so that a caller that builds a PublicKey cannot reach the verifier with
	// no point. It does not rely on check or on Marshal.
	if key.ECDSA == nil {
		return Result{}, fmt.Errorf("%w: ECDSA key without a point", ErrMalformed)
	}

	// Defense in depth: check refuses a point that is not on the curve; this
	// check stays so that a caller that builds a PublicKey cannot reach the
	// verifier with a point off the curve, with no coordinates or with the
	// point at infinity.
	if !pointOnCurve(key.ECDSA) {
		return Result{}, fmt.Errorf("%w: ECDSA point is not on the curve", ErrMalformed)
	}

	br := reader{b: s.Blob, err: nil}
	rv := br.mpint()
	sv := br.mpint()

	if err := br.end(); err != nil {
		return Result{}, err
	}

	n := elliptic.P256().Params().N

	// Defense in depth: VerifyASN1 checks the range too; this check stays so
	// that a change of the library cannot accept a zero or a large value.
	if rv.Sign() == 0 || sv.Sign() == 0 || rv.Cmp(n) >= 0 || sv.Cmp(n) >= 0 {
		return Result{}, fmt.Errorf("%w: r or s out of range", ErrVerify)
	}

	der, err := asn1.Marshal(struct{ R, S *big.Int }{R: rv, S: sv})
	if err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrVerify, err)
	}

	digest := sha256.Sum256(signed)

	if !ecdsa.VerifyASN1(key.ECDSA, digest[:], der) {
		return Result{}, ErrVerify
	}

	return Result{UserPresent: false, UserVerified: false, Counter: 0}, nil
}

// Verify parses the public key wire and the armored signature, and checks
// that the signature is a signature of the key over message in the
// namespace namespace.
func Verify(wire []byte, namespace string, message, armored []byte) (Result, error) {
	key, err := ParsePublicKey(wire)
	if err != nil {
		return Result{}, err
	}

	sig, err := Parse(armored)
	if err != nil {
		return Result{}, err
	}

	return sig.Verify(key, namespace, message)
}

// signedData returns the data that the signer signs.
func signedData(namespace, hashAlgorithm string, digest []byte) []byte {
	b := []byte(magic)
	b = appendString(b, []byte(namespace))
	b = appendString(b, []byte{})
	b = appendString(b, []byte(hashAlgorithm))

	return appendString(b, digest)
}

// hashMessage returns the hash of message with an allowed algorithm.
func hashMessage(algorithm string, message []byte) ([]byte, error) {
	switch algorithm {
	case HashSHA256:
		d := sha256.Sum256(message)
		return d[:], nil
	case HashSHA512:
		d := sha512.Sum512(message)
		return d[:], nil
	default:
		return nil, fmt.Errorf("%w: %q", ErrHashAlgorithm, printable(algorithm))
	}
}

// allowedHash reports whether algorithm is in the allowlist.
func allowedHash(algorithm string) bool {
	return algorithm == HashSHA256 || algorithm == HashSHA512
}

// appendString appends v as an SSH string: a 32-bit length and the bytes.
func appendString(b, v []byte) []byte {
	n := len(v)
	if n < 0 || n > math.MaxUint32 {
		// Each value of the package is far below this size.
		panic("sshsig: value too long for an SSH string")
	}

	b = binary.BigEndian.AppendUint32(b, uint32(n))

	return append(b, v...)
}

// isToken reports whether s has 1 to maxLen bytes of printable ASCII
// without a space.
func isToken(s string, maxLen int) bool {
	if s == "" || len(s) > maxLen {
		return false
	}

	for i := range len(s) {
		if s[i] <= ' ' || s[i] > '~' {
			return false
		}
	}

	return true
}

// isBase64 reports whether s holds only characters of standard base64.
func isBase64(s string) bool {
	for i := range len(s) {
		c := s[i]
		if (c < 'A' || c > 'Z') && (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '+' && c != '/' && c != '=' {
			return false
		}
	}

	return true
}

// printable returns s with each byte outside printable ASCII as an escape,
// so that an error text cannot hold a control character.
func printable(s string) string {
	const maxShown = 64
	if len(s) > maxShown {
		s = s[:maxShown]
	}

	return strings.Trim(fmt.Sprintf("%+q", s), `"`)
}

// reader reads SSH wire values. After the first fault each read returns
// zero values, and err holds the fault.
type reader struct {
	b   []byte
	err error
}

// take returns the next n bytes.
func (r *reader) take(n int) []byte {
	if r.err != nil {
		return nil
	}

	if n < 0 || n > len(r.b) {
		r.err = fmt.Errorf("%w: truncated", ErrMalformed)
		return nil
	}

	v := r.b[:n]
	r.b = r.b[n:]

	return v
}

// byte returns the next byte.
func (r *reader) byte() byte {
	v := r.take(1)
	if v == nil {
		return 0
	}

	return v[0]
}

// uint32 returns the next 32-bit big-endian value.
func (r *reader) uint32() uint32 {
	const size = 4

	v := r.take(size)
	if v == nil {
		return 0
	}

	return binary.BigEndian.Uint32(v)
}

// str returns the next SSH string. A length above maxLen is malformed.
func (r *reader) str(maxLen int) []byte {
	n := r.uint32()
	if r.err != nil {
		return nil
	}

	if int64(n) > int64(maxLen) {
		r.err = fmt.Errorf("%w: field of %d bytes", ErrMalformed, n)
		return nil
	}

	return r.take(int(n))
}

// mpint returns the next SSH mpint as a non-negative value. It refuses a
// negative value, a 0x00 prefix that the next byte does not need, and a
// value of more than maxMPIntSize bytes. An empty mpint is zero. After a
// fault it returns zero, and err holds the fault.
func (r *reader) mpint() *big.Int {
	v := new(big.Int)

	b := r.str(maxMPIntSize)
	if r.err != nil {
		return v
	}

	if len(b) > 0 && b[0]&0x80 != 0 {
		r.err = fmt.Errorf("%w: negative mpint", ErrMalformed)
		return v
	}

	if len(b) > 1 && b[0] == 0 && b[1]&0x80 == 0 {
		r.err = fmt.Errorf("%w: mpint with a zero byte that is not needed", ErrMalformed)
		return v
	}

	return v.SetBytes(b)
}

// end returns the first fault, or ErrMalformed for bytes after the last
// value.
func (r *reader) end() error {
	if r.err != nil {
		return r.err
	}

	if len(r.b) != 0 {
		return fmt.Errorf("%w: %d bytes after the last field", ErrMalformed, len(r.b))
	}

	return nil
}

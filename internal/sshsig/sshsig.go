// Package sshsig parses and verifies signatures in the format that
// "ssh-keygen -Y sign" writes (the SSHSIG format of OpenSSH). It accepts the
// key types ssh-ed25519 and sk-ssh-ed25519@openssh.com, and the hash
// algorithms sha256 and sha512. It refuses each other value.
//
// A signature holds the public key of the signer. Verify compares that key
// with the key that the caller gives and verifies with the caller's key
// only.
package sshsig

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
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
	magic              = "SSHSIG"
	version            = 1
	applicationPrefix  = "ssh:"
	armorBegin         = "-----BEGIN SSH SIGNATURE-----"
	armorEnd           = "-----END SSH SIGNATURE-----"
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
	// Type is TypeEd25519 or TypeSKEd25519.
	Type string
	// Key is the Ed25519 public key.
	Key ed25519.PublicKey
	// Application is the application of a security key, for example
	// "ssh:". It is empty for TypeEd25519.
	Application string
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
	// Blob is the Ed25519 signature.
	Blob []byte
	// Flags and Counter are the flags and the signature counter of a
	// security key signature. Both are 0 for TypeEd25519.
	Flags   byte
	Counter uint32
}

// Result is what a verified signature says about the key.
type Result struct {
	// UserPresent and UserVerified are the flags of a security key
	// signature. Both are false for TypeEd25519. A software middleware can
	// set them, so they prove no hardware.
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
	key := r.str(ed25519KeySize)

	var app string

	switch keyType {
	case TypeEd25519:
	case TypeSKEd25519:
		app = string(r.str(MaxApplicationSize))
	default:
		if r.err != nil {
			return PublicKey{}, r.err
		}

		return PublicKey{}, fmt.Errorf("%w: %q", ErrKeyType, printable(keyType))
	}

	if err := r.end(); err != nil {
		return PublicKey{}, err
	}

	if len(key) != ed25519KeySize {
		return PublicKey{}, fmt.Errorf("%w: Ed25519 key of %d bytes", ErrMalformed, len(key))
	}

	pk := PublicKey{Type: keyType, Key: ed25519.PublicKey(bytes.Clone(key)), Application: app}
	if err := pk.check(); err != nil {
		return PublicKey{}, err
	}

	return pk, nil
}

// Marshal returns the key in its SSH wire form.
func (k PublicKey) Marshal() []byte {
	const lengths = 3 * 4

	b := make([]byte, 0, lengths+len(k.Type)+len(k.Key)+len(k.Application))
	b = appendString(b, []byte(k.Type))
	b = appendString(b, k.Key)

	if k.Type == TypeSKEd25519 {
		b = appendString(b, []byte(k.Application))
	}

	return b
}

// check checks the fields of a key.
func (k PublicKey) check() error {
	if len(k.Key) != ed25519KeySize {
		return fmt.Errorf("%w: Ed25519 key of %d bytes", ErrMalformed, len(k.Key))
	}

	switch k.Type {
	case TypeEd25519:
		if k.Application != "" {
			return fmt.Errorf("%w: application on a key that is not a security key", ErrMalformed)
		}
	case TypeSKEd25519:
		// ssh-keygen only enrolls an application that starts with "ssh:".
		// Other applications are web origins, and the package refuses them.
		if !strings.HasPrefix(k.Application, applicationPrefix) || !isToken(k.Application, MaxApplicationSize) {
			return fmt.Errorf("%w: application %q", ErrMalformed, printable(k.Application))
		}
	default:
		return fmt.Errorf("%w: %q", ErrKeyType, printable(k.Type))
	}

	return nil
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

// parseInner parses the signature field: the signature type, the Ed25519
// signature, and for a security key the flags and the counter. It returns
// a Signature with these fields only.
func parseInner(inner []byte) (Signature, error) {
	r := reader{b: inner, err: nil}
	sigType := string(r.str(maxTypeSize))
	blob := bytes.Clone(r.str(ed25519SigSize))

	var (
		flags   byte
		counter uint32
	)

	switch sigType {
	case TypeEd25519:
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

	if len(blob) != ed25519SigSize {
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

	if len(s.Blob) != ed25519SigSize {
		return Result{}, fmt.Errorf("%w: Ed25519 signature of %d bytes", ErrMalformed, len(s.Blob))
	}

	signed := signedData(namespace, s.HashAlgorithm, digest)

	if key.Type == TypeEd25519 {
		if s.Flags != 0 || s.Counter != 0 {
			return Result{}, fmt.Errorf("%w: flags on a key that is not a security key", ErrFlags)
		}

		if !ed25519.Verify(key.Key, signed, s.Blob) {
			return Result{}, ErrVerify
		}

		return Result{UserPresent: false, UserVerified: false, Counter: 0}, nil
	}

	return s.verifySK(key, signed)
}

// verifySK verifies a security key signature. The security key signs the
// SHA-256 of the application, the flags, the counter and the SHA-256 of the
// signed data.
func (s Signature) verifySK(key PublicKey, signed []byte) (Result, error) {
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

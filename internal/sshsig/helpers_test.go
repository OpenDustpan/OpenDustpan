package sshsig_test

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/binary"
	"io/fs"
	"math"
	"os"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/sshsig"
	"opendustpan.org/opendustpan/internal/stepup"
)

// fixture returns the bytes of a file in testdata. generate.sh in
// testdata/gen made each file with the real ssh-keygen.
func fixture(t *testing.T, name string) []byte {
	t.Helper()

	b, err := fs.ReadFile(os.DirFS("testdata"), name)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	return b
}

// publicKey returns the type and the wire form of a .pub fixture.
func publicKey(t *testing.T, name string) (string, []byte) {
	t.Helper()

	fields := strings.Fields(string(fixture(t, name+".pub")))

	const minFields = 2
	if len(fields) < minFields {
		t.Fatalf("fixture %s.pub has %d fields", name, len(fields))
	}

	wire, err := base64.StdEncoding.DecodeString(fields[1])
	if err != nil {
		t.Fatalf("decode fixture %s.pub: %v", name, err)
	}

	return fields[0], wire
}

// result returns a sshsig.Result.
func result(userPresent, userVerified bool, counter uint32) sshsig.Result {
	return sshsig.Result{UserPresent: userPresent, UserVerified: userVerified, Counter: counter}
}

// assertion returns a stepup.Assertion.
func assertion(userPresent, userVerified bool, counter uint32) stepup.Assertion {
	return stepup.Assertion{UserPresent: userPresent, UserVerified: userVerified, Counter: counter}
}

// testKey is an Ed25519 key that a test makes, for signatures that
// ssh-keygen does not write.
type testKey struct {
	priv ed25519.PrivateKey
	pub  sshsig.PublicKey
}

// newKey returns a new key of the type keyType. An sk- key has the
// application "ssh:".
func newKey(t *testing.T, keyType string) testKey {
	t.Helper()

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	k := sshsig.PublicKey{Type: keyType, Key: pub, Application: ""}
	if keyType == sshsig.TypeSKEd25519 {
		k.Application = "ssh:"
	}

	return testKey{priv: priv, pub: k}
}

// spec is a signature that a test builds field by field. The sign fields
// are the values that the signature covers. They can differ from the
// fields in the blob.
type spec struct {
	magic     string
	version   uint32
	publicKey []byte
	namespace string
	reserved  []byte
	hash      string
	sigType   string
	// sigBlob replaces the Ed25519 signature when it is not nil.
	sigBlob []byte
	flags   byte
	counter uint32
	// innerTail and tail are bytes after the inner signature and after the
	// blob.
	innerTail []byte
	tail      []byte
	// inner replaces the whole signature field when it is not nil.
	inner []byte

	signNamespace string
	signHash      string
	signMessage   []byte
	signFlags     byte
}

// valid returns the spec of a valid signature of k over message in the
// namespace sshsig.NamespaceStepUp with sha512. An sk- signature has user
// presence and the counter 9.
func (k testKey) valid(message []byte) spec {
	const counter = 9

	s := spec{
		magic:         "SSHSIG",
		version:       1,
		publicKey:     k.pub.Marshal(),
		namespace:     sshsig.NamespaceStepUp,
		reserved:      nil,
		hash:          sshsig.HashSHA512,
		sigType:       k.pub.Type,
		sigBlob:       nil,
		flags:         0,
		counter:       0,
		innerTail:     nil,
		tail:          nil,
		inner:         nil,
		signNamespace: sshsig.NamespaceStepUp,
		signHash:      sshsig.HashSHA512,
		signMessage:   message,
		signFlags:     0,
	}

	if k.pub.Type == sshsig.TypeSKEd25519 {
		s.flags = sshsig.FlagUserPresent
		s.signFlags = sshsig.FlagUserPresent
		s.counter = counter
	}

	return s
}

// blob returns the bytes of the signature of s by k.
func (k testKey) blob(s spec) []byte {
	b := []byte(s.magic)
	b = binary.BigEndian.AppendUint32(b, s.version)
	b = appendString(b, s.publicKey)
	b = appendString(b, []byte(s.namespace))
	b = appendString(b, s.reserved)
	b = appendString(b, []byte(s.hash))

	inner := s.inner
	if inner == nil {
		inner = k.innerSig(s)
	}

	b = appendString(b, inner)

	return append(b, s.tail...)
}

// armored returns the signature of s by k with the armor of ssh-keygen.
func (k testKey) armored(s spec) []byte {
	return armor(k.blob(s))
}

// innerSig returns the signature field of s.
func (k testKey) innerSig(s spec) []byte {
	sig := s.sigBlob
	if sig == nil {
		sig = k.sign(s)
	}

	b := appendString(nil, []byte(s.sigType))
	b = appendString(b, sig)

	if s.sigType == sshsig.TypeSKEd25519 {
		b = append(b, s.flags)
		b = binary.BigEndian.AppendUint32(b, s.counter)
	}

	return append(b, s.innerTail...)
}

// sign signs the signed data of s, as ssh-keygen and a security key do.
func (k testKey) sign(s spec) []byte {
	var digest []byte

	if s.signHash == sshsig.HashSHA256 {
		d := sha256.Sum256(s.signMessage)
		digest = d[:]
	} else {
		d := sha512.Sum512(s.signMessage)
		digest = d[:]
	}

	signed := []byte("SSHSIG")
	signed = appendString(signed, []byte(s.signNamespace))
	signed = appendString(signed, nil)
	signed = appendString(signed, []byte(s.signHash))
	signed = appendString(signed, digest)

	if k.pub.Type != sshsig.TypeSKEd25519 {
		return ed25519.Sign(k.priv, signed)
	}

	app := sha256.Sum256([]byte(k.pub.Application))
	data := sha256.Sum256(signed)
	tbs := append(app[:], s.signFlags)
	tbs = binary.BigEndian.AppendUint32(tbs, s.counter)
	tbs = append(tbs, data[:]...)

	return ed25519.Sign(k.priv, tbs)
}

// armor writes blob with the armor of ssh-keygen: lines of 70 characters.
func armor(blob []byte) []byte {
	const lineSize = 70

	enc := base64.StdEncoding.EncodeToString(blob)

	var b bytes.Buffer

	b.WriteString("-----BEGIN SSH SIGNATURE-----\n")

	for len(enc) > lineSize {
		b.WriteString(enc[:lineSize] + "\n")
		enc = enc[lineSize:]
	}

	b.WriteString(enc + "\n-----END SSH SIGNATURE-----\n")

	return b.Bytes()
}

// appendString appends v as an SSH string.
func appendString(b, v []byte) []byte {
	n := len(v)
	if n > math.MaxUint32 {
		panic("test value too long for an SSH string")
	}

	b = binary.BigEndian.AppendUint32(b, uint32(n))

	return append(b, v...)
}

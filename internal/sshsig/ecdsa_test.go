package sshsig_test

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/asn1"
	"encoding/binary"
	"errors"
	"math/big"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/sshsig"
	"opendustpan.org/opendustpan/internal/stepup"
)

// ecdsaTestKey returns a fixed P-256 key, so that the seeds and the tests
// use the same key on each run.
func ecdsaTestKey(tb testing.TB) *ecdsa.PrivateKey {
	tb.Helper()

	d := make([]byte, 32)
	for i := range d {
		d[i] = byte(i + 1)
	}

	k, err := ecdsa.ParseRawPrivateKey(elliptic.P256(), d)
	if err != nil {
		tb.Fatalf("parse test key: %v", err)
	}

	return k
}

// ecdsaWire returns the wire form of the P-256 public key pub.
func ecdsaWire(tb testing.TB, pub *ecdsa.PublicKey) []byte {
	tb.Helper()

	point, err := pub.Bytes()
	if err != nil {
		tb.Fatalf("encode test key: %v", err)
	}

	b := appendString(nil, []byte(sshsig.TypeECDSAP256))
	b = appendString(b, []byte("nistp256"))

	return appendString(b, point)
}

// mpint returns the SSH mpint of n: its bytes, with a 0x00 prefix when the
// high bit of the first byte is set. Zero is the empty string.
func mpint(n *big.Int) []byte {
	raw := n.Bytes()
	if len(raw) > 0 && raw[0]&0x80 != 0 {
		raw = append([]byte{0}, raw...)
	}

	return appendString(nil, raw)
}

// digestOf returns the hash of msg with algorithm.
func digestOf(algorithm string, msg []byte) []byte {
	if algorithm == sshsig.HashSHA256 {
		d := sha256.Sum256(msg)
		return d[:]
	}

	d := sha512.Sum512(msg)

	return d[:]
}

// ecdsaOpts sets the fields of the signature that ecdsaBlob builds.
type ecdsaOpts struct {
	// namespace is the namespace of the signature.
	namespace string
	// hash is the hash algorithm. It is sshsig.HashSHA512 when empty.
	hash string
	// sigType is the signature type. It is sshsig.TypeECDSAP256 when empty.
	sigType string
	// rawBlob replaces the signature blob when it is not nil. No signature
	// is made then.
	rawBlob []byte
}

// ecdsaSign signs signed with k and returns the blob: the mpint r and the
// mpint s.
func ecdsaSign(tb testing.TB, k *ecdsa.PrivateKey, signed []byte) []byte {
	tb.Helper()

	digest := sha256.Sum256(signed)

	der, err := ecdsa.SignASN1(rand.Reader, k, digest[:])
	if err != nil {
		tb.Fatalf("sign: %v", err)
	}

	var sig struct{ R, S *big.Int }
	if _, err := asn1.Unmarshal(der, &sig); err != nil {
		tb.Fatalf("parse signature: %v", err)
	}

	return append(mpint(sig.R), mpint(sig.S)...)
}

// ecdsaBlob returns the bytes of an SSHSIG signature of testMessage by k,
// with the options o.
func ecdsaBlob(tb testing.TB, k *ecdsa.PrivateKey, o ecdsaOpts) []byte {
	tb.Helper()

	hash := o.hash
	if hash == "" {
		hash = sshsig.HashSHA512
	}

	sigType := o.sigType
	if sigType == "" {
		sigType = sshsig.TypeECDSAP256
	}

	signed := []byte("SSHSIG")
	signed = appendString(signed, []byte(o.namespace))
	signed = appendString(signed, nil)
	signed = appendString(signed, []byte(hash))
	signed = appendString(signed, digestOf(hash, testMessage()))

	blob := o.rawBlob
	if blob == nil {
		blob = ecdsaSign(tb, k, signed)
	}

	inner := appendString(nil, []byte(sigType))
	inner = appendString(inner, blob)

	b := []byte("SSHSIG")
	b = binary.BigEndian.AppendUint32(b, 1)
	b = appendString(b, ecdsaWire(tb, &k.PublicKey))
	b = appendString(b, []byte(o.namespace))
	b = appendString(b, nil)
	b = appendString(b, []byte(hash))

	return appendString(b, inner)
}

// ecdsaArmored returns the armored signature of testMessage by k, with the
// options o.
func ecdsaArmored(tb testing.TB, k *ecdsa.PrivateKey, o ecdsaOpts) []byte {
	tb.Helper()

	return armor(ecdsaBlob(tb, k, o))
}

// ecdsaSeedSignature returns an armored ECDSA signature for the fuzz seeds.
func ecdsaSeedSignature(tb testing.TB) []byte {
	tb.Helper()

	return ecdsaArmored(tb, ecdsaTestKey(tb), ecdsaOpts{
		namespace: sshsig.NamespaceStepUp,
		hash:      sshsig.HashSHA512,
		sigType:   sshsig.TypeECDSAP256,
		rawBlob:   nil,
	})
}

// ecdsaSeedKey returns the wire of the ECDSA key for the fuzz seeds.
func ecdsaSeedKey(tb testing.TB) []byte {
	tb.Helper()

	return ecdsaWire(tb, &ecdsaTestKey(tb).PublicKey)
}

// TestParsePublicKeyECDSA sends hostile P-256 keys to the key parser.
func TestParsePublicKeyECDSA(t *testing.T) {
	t.Parallel()

	k := ecdsaTestKey(t)
	point, err := k.PublicKey.Bytes()
	if err != nil {
		t.Fatalf("encode test key: %v", err)
	}

	build := func(fields ...[]byte) []byte {
		var b []byte
		for _, f := range fields {
			b = appendString(b, f)
		}

		return b
	}

	typ := []byte(sshsig.TypeECDSAP256)
	curve := []byte("nistp256")

	compressed := append([]byte{0x02}, point[1:33]...)
	yPlusOne := append([]byte(nil), point...)
	yPlusOne[len(yPlusOne)-1]++

	tests := []struct {
		name string
		wire []byte
		want error
	}{
		{"a P-256 key", ecdsaWire(t, &k.PublicKey), nil},
		{"the curve name nistp384", build(typ, []byte("nistp384"), point), sshsig.ErrMalformed},
		{"a point of zero bytes", build(typ, curve, make([]byte, 65)), sshsig.ErrMalformed},
		{"the point at infinity", build(typ, curve, append([]byte{0x04}, make([]byte, 64)...)), sshsig.ErrMalformed},
		{"a compressed point", build(typ, curve, compressed), sshsig.ErrMalformed},
		{"a point of 64 bytes", build(typ, curve, point[:64]), sshsig.ErrMalformed},
		{"a point of one zero byte", build(typ, curve, []byte{0}), sshsig.ErrMalformed},
		{"a point with Y changed by 1", build(typ, curve, yPlusOne), sshsig.ErrMalformed},
		{"an empty point", build(typ, curve, nil), sshsig.ErrMalformed},
		{"a byte after the point", append(ecdsaWire(t, &k.PublicKey), 0), sshsig.ErrMalformed},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			checkParsePublicKey(t, tc.wire, tc.want)
		})
	}
}

// checkParsePublicKey parses wire and checks the error. For a key that
// parses, it checks the fields and that Marshal gives the wire bytes.
func checkParsePublicKey(t *testing.T, wire []byte, want error) {
	t.Helper()

	got, err := sshsig.ParsePublicKey(wire)
	if !errors.Is(err, want) || (err == nil) != (want == nil) {
		t.Fatalf("ParsePublicKey error = %v, want %v", err, want)
	}

	if err != nil {
		return
	}

	if got.Type != sshsig.TypeECDSAP256 || got.ECDSA == nil || len(got.Key) != 0 {
		t.Fatalf("ParsePublicKey returned %+v for a P-256 key", got)
	}

	if !bytes.Equal(got.Marshal(), wire) {
		t.Fatal("Marshal does not give the parsed bytes")
	}
}

// TestVerifyECDSA sends P-256 signatures to Verify, with the hostile values
// of each check.
func TestVerifyECDSA(t *testing.T) {
	t.Parallel()

	signer := ecdsaTestKey(t)
	signerWire := ecdsaWire(t, &signer.PublicKey)

	other, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	n := elliptic.P256().Params().N
	one := big.NewInt(1)
	msg := testMessage()

	tests := []struct {
		name      string
		opts      ecdsaOpts
		namespace string
		message   []byte
		key       *ecdsa.PublicKey
		want      error
	}{
		{
			"a valid sha512 signature",
			ecdsaOpts{namespace: sshsig.NamespaceStepUp, hash: sshsig.HashSHA512, sigType: "", rawBlob: nil},
			sshsig.NamespaceStepUp, msg, nil, nil,
		},
		{
			"a valid sha256 signature",
			ecdsaOpts{namespace: sshsig.NamespaceStepUp, hash: sshsig.HashSHA256, sigType: "", rawBlob: nil},
			sshsig.NamespaceStepUp, msg, nil, nil,
		},
		{
			"a valid key-add signature",
			ecdsaOpts{namespace: sshsig.NamespaceKeyAdd, hash: "", sigType: "", rawBlob: nil},
			sshsig.NamespaceKeyAdd, msg, nil, nil,
		},
		{
			"a signature for another namespace",
			ecdsaOpts{namespace: sshsig.NamespaceStepUp, hash: "", sigType: "", rawBlob: nil},
			sshsig.NamespaceKeyAdd, msg, nil, sshsig.ErrNamespace,
		},
		{
			"r of zero",
			ecdsaOpts{
				namespace: sshsig.NamespaceStepUp, hash: "", sigType: "",
				rawBlob: append(mpint(new(big.Int)), mpint(one)...),
			},
			sshsig.NamespaceStepUp, msg, nil, sshsig.ErrVerify,
		},
		{
			"s of zero",
			ecdsaOpts{
				namespace: sshsig.NamespaceStepUp, hash: "", sigType: "",
				rawBlob: append(mpint(one), mpint(new(big.Int))...),
			},
			sshsig.NamespaceStepUp, msg, nil, sshsig.ErrVerify,
		},
		{
			"s equal to the order",
			ecdsaOpts{
				namespace: sshsig.NamespaceStepUp, hash: "", sigType: "",
				rawBlob: append(mpint(one), mpint(n)...),
			},
			sshsig.NamespaceStepUp, msg, nil, sshsig.ErrVerify,
		},
		{
			"r above the order",
			ecdsaOpts{
				namespace: sshsig.NamespaceStepUp, hash: "", sigType: "",
				rawBlob: append(mpint(new(big.Int).Add(n, one)), mpint(one)...),
			},
			sshsig.NamespaceStepUp, msg, nil, sshsig.ErrVerify,
		},
		{
			"a negative r",
			ecdsaOpts{
				namespace: sshsig.NamespaceStepUp, hash: "", sigType: "",
				rawBlob: append(appendString(nil, []byte{0x80, 0x01}), mpint(one)...),
			},
			sshsig.NamespaceStepUp, msg, nil, sshsig.ErrMalformed,
		},
		{
			"an mpint with a zero byte that is not needed",
			ecdsaOpts{
				namespace: sshsig.NamespaceStepUp, hash: "", sigType: "",
				rawBlob: append(appendString(nil, []byte{0x00, 0x01}), mpint(one)...),
			},
			sshsig.NamespaceStepUp, msg, nil, sshsig.ErrMalformed,
		},
		{
			"an mpint of 34 bytes",
			ecdsaOpts{
				namespace: sshsig.NamespaceStepUp, hash: "", sigType: "",
				rawBlob: append(appendString(nil, bytes.Repeat([]byte{0x01}, 34)), mpint(one)...),
			},
			sshsig.NamespaceStepUp, msg, nil, sshsig.ErrMalformed,
		},
		{
			"a byte after s",
			ecdsaOpts{
				namespace: sshsig.NamespaceStepUp, hash: "", sigType: "",
				rawBlob: append(append(mpint(one), mpint(one)...), 0),
			},
			sshsig.NamespaceStepUp, msg, nil, sshsig.ErrMalformed,
		},
		{
			"a blob of 75 bytes",
			ecdsaOpts{
				namespace: sshsig.NamespaceStepUp, hash: "", sigType: "",
				rawBlob: make([]byte, 75),
			},
			sshsig.NamespaceStepUp, msg, nil, sshsig.ErrMalformed,
		},
		{
			"a signature by another key",
			ecdsaOpts{namespace: sshsig.NamespaceStepUp, hash: "", sigType: "", rawBlob: nil},
			sshsig.NamespaceStepUp, msg, &other.PublicKey, sshsig.ErrKeyMismatch,
		},
		{
			"another message",
			ecdsaOpts{namespace: sshsig.NamespaceStepUp, hash: "", sigType: "", rawBlob: nil},
			sshsig.NamespaceStepUp, []byte("other"), nil, sshsig.ErrVerify,
		},
		{
			"an ed25519 signature type for an ECDSA key",
			ecdsaOpts{
				namespace: sshsig.NamespaceStepUp, hash: "",
				sigType: sshsig.TypeEd25519, rawBlob: make([]byte, 64),
			},
			sshsig.NamespaceStepUp, msg, nil, sshsig.ErrKeyType,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			wire := signerWire
			if tc.key != nil {
				wire = ecdsaWire(t, tc.key)
			}

			armored := ecdsaArmored(t, signer, tc.opts)

			got, err := sshsig.Verify(wire, tc.namespace, tc.message, armored)
			if !errors.Is(err, tc.want) || (err == nil) != (tc.want == nil) {
				t.Fatalf("Verify error = %v, want %v", err, tc.want)
			}

			if got != (sshsig.Result{UserPresent: false, UserVerified: false, Counter: 0}) {
				t.Fatalf("Verify result = %+v, want the zero result", got)
			}
		})
	}
}

// TestVerifierECDSA checks the step-up verifier with a P-256 key.
func TestVerifierECDSA(t *testing.T) {
	t.Parallel()

	signer := ecdsaTestKey(t)

	tests := []struct {
		name string
		edit func(k *stepup.Key)
		// flip changes the last byte of the signature blob.
		flip bool
		want error
	}{
		{"a valid signature", func(*stepup.Key) {}, false, nil},
		{"a stored type that differs from the key", func(k *stepup.Key) {
			k.Type = sshsig.TypeEd25519
		}, false, stepup.ErrInvalid},
		{"a changed signature byte", func(*stepup.Key) {}, true, stepup.ErrBadAnswer},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			k := stepup.Key{
				ID:          "stk-ecdsa",
				PersonID:    "person",
				Factor:      stepup.FactorSSH,
				Type:        sshsig.TypeECDSAP256,
				Fingerprint: "",
				PublicKey:   ecdsaWire(t, &signer.PublicKey),
				Name:        "ecdsa",
				Counter:     0,
				Suspended:   false,
				EnrolledBy:  "",
				CreatedAt:   time.Unix(0, 0),
			}
			tc.edit(&k)

			blob := ecdsaBlob(t, signer, ecdsaOpts{
				namespace: sshsig.NamespaceStepUp,
				hash:      sshsig.HashSHA512,
				sigType:   sshsig.TypeECDSAP256,
				rawBlob:   nil,
			})
			if tc.flip {
				blob[len(blob)-1] ^= 0x01
			}

			got, err := sshsig.NewVerifier().Verify(t.Context(), k, stepup.ScopeWrite, testMessage(), armor(blob))
			if !errors.Is(err, tc.want) || (err == nil) != (tc.want == nil) {
				t.Fatalf("Verify error = %v, want %v", err, tc.want)
			}

			if got != assertion(false, false, 0) {
				t.Fatalf("Verify returned the assertion %+v", got)
			}
		})
	}
}

// TestSignatureVerifyECDSAFlags builds exported signatures by hand, with the
// flags or the counter that an ECDSA signature never has, and sends each one
// to Verify. Parse never sets them for this type, so these rows reach the
// check that a caller-built Signature must not skip.
func TestSignatureVerifyECDSAFlags(t *testing.T) {
	t.Parallel()

	signer := ecdsaTestKey(t)

	key, err := sshsig.ParsePublicKey(ecdsaWire(t, &signer.PublicKey))
	if err != nil {
		t.Fatalf("ParsePublicKey: %v", err)
	}

	armored := ecdsaArmored(t, signer, ecdsaOpts{
		namespace: sshsig.NamespaceStepUp,
		hash:      sshsig.HashSHA512,
		sigType:   "",
		rawBlob:   nil,
	})

	tests := []struct {
		name string
		edit func(s *sshsig.Signature)
		want error
	}{
		{"a signature with no flags and no counter", func(*sshsig.Signature) {}, nil},
		{"a flag of user presence", func(s *sshsig.Signature) {
			s.Flags = sshsig.FlagUserPresent
		}, sshsig.ErrFlags},
		{"a flag outside the allowlist", func(s *sshsig.Signature) {
			s.Flags = 0xff
		}, sshsig.ErrFlags},
		{"a counter of one", func(s *sshsig.Signature) {
			s.Counter = 1
		}, sshsig.ErrFlags},
		{"a flag and a counter", func(s *sshsig.Signature) {
			s.Flags = sshsig.FlagUserPresent
			s.Counter = 1
		}, sshsig.ErrFlags},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s, err := sshsig.Parse(armored)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}

			tc.edit(&s)

			got, err := s.Verify(key, sshsig.NamespaceStepUp, testMessage())
			if !errors.Is(err, tc.want) || (err == nil) != (tc.want == nil) {
				t.Fatalf("Verify error = %v, want %v", err, tc.want)
			}

			if got != (sshsig.Result{UserPresent: false, UserVerified: false, Counter: 0}) {
				t.Fatalf("Verify result = %+v, want the zero result", got)
			}
		})
	}
}

package sshsig_test

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"errors"
	"math/big"
	"reflect"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/sshsig"
)

// Each test in this file sends hostile input to one layer of the ECDSA path,
// as if every other layer had failed. A layer that another layer also stops
// is called through a hook of hooks_internal_test.go, or it is told apart by the text
// of its error.

// ecdsaKey returns a PublicKey of TypeECDSAP256 with the point pub.
func ecdsaKey(pub *ecdsa.PublicKey) sshsig.PublicKey {
	return sshsig.PublicKey{Type: sshsig.TypeECDSAP256, Key: nil, Application: "", ECDSA: pub}
}

// rawKey returns a P-256 public key with the coordinates x and y, with no
// check. It is the only place that builds a key by hand: ParseUncompressedPublicKey
// refuses every key that these tests need, and the fields X and Y are
// deprecated, so reflection sets them. A nil value leaves the field nil.
func rawKey(x, y *big.Int) *ecdsa.PublicKey {
	pub := new(ecdsa.PublicKey)
	v := reflect.ValueOf(pub).Elem()

	v.FieldByName("Curve").Set(reflect.ValueOf(elliptic.P256()))

	if x != nil {
		v.FieldByName("X").Set(reflect.ValueOf(x))
	}

	if y != nil {
		v.FieldByName("Y").Set(reflect.ValueOf(y))
	}

	return pub
}

// coord returns the coordinate of pub at point[from:to]: X is 1:33, Y is 33:65.
func coord(tb testing.TB, pub *ecdsa.PublicKey, from, to int) *big.Int {
	tb.Helper()

	point, err := pub.Bytes()
	if err != nil {
		tb.Fatalf("encode key: %v", err)
	}

	return new(big.Int).SetBytes(point[from:to])
}

// coordX returns the X coordinate of pub.
func coordX(tb testing.TB, pub *ecdsa.PublicKey) *big.Int {
	tb.Helper()

	return coord(tb, pub, 1, 33)
}

// coordY returns the Y coordinate of pub.
func coordY(tb testing.TB, pub *ecdsa.PublicKey) *big.Int {
	tb.Helper()

	return coord(tb, pub, 33, 65)
}

// TestCheckECDSALayers calls the key check alone with keys that a caller
// builds by hand.
func TestCheckECDSALayers(t *testing.T) {
	t.Parallel()

	good := &ecdsaTestKey(t).PublicKey
	gx, gy := coordX(t, good), coordY(t, good)
	fieldPrime := elliptic.P256().Params().P

	p384, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatalf("generate P-384 key: %v", err)
	}

	withFields := ecdsaKey(good)
	withFields.Key = bytes.Repeat([]byte{1}, 32)

	withApp := ecdsaKey(good)
	withApp.Application = "ssh:"

	tests := []struct {
		name string
		key  sshsig.PublicKey
		want string // text of the error, or "" for no error
	}{
		{"a valid key", ecdsaKey(good), ""},
		{"no point", ecdsaKey(nil), "not on P-256"},
		{"a point on P-384", ecdsaKey(&p384.PublicKey), "not on P-256"},
		{"no X", ecdsaKey(rawKey(nil, gy)), "not on the curve"},
		{"no Y", ecdsaKey(rawKey(gx, nil)), "not on the curve"},
		{"a point off the curve", ecdsaKey(rawKey(big.NewInt(1), big.NewInt(2))), "not on the curve"},
		{
			"the point with Y changed by 1",
			ecdsaKey(rawKey(gx, new(big.Int).Add(gy, big.NewInt(1)))),
			"not on the curve",
		},
		{"the point at infinity", ecdsaKey(rawKey(new(big.Int), new(big.Int))), "not on the curve"},
		{"a negative X", ecdsaKey(rawKey(new(big.Int).Neg(gx), gy)), "not on the curve"},
		{"X above the field", ecdsaKey(rawKey(new(big.Int).Add(gx, fieldPrime), gy)), "not on the curve"},
		{"Ed25519 key bytes", withFields, "Ed25519 fields"},
		{"an application", withApp, "Ed25519 fields"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var want error
			if tc.want != "" {
				want = sshsig.ErrMalformed
			}

			checkLayerError(t, sshsig.CheckECDSA(tc.key), tc.want, want)
		})
	}
}

// TestVerifyRefusesHandBuiltKey sends keys that a caller builds by hand to
// Verify. The key check is the layer that stops them.
func TestVerifyRefusesHandBuiltKey(t *testing.T) {
	t.Parallel()

	signer := ecdsaTestKey(t)
	armored := ecdsaArmored(t, signer, ecdsaOpts{
		namespace: sshsig.NamespaceStepUp, hash: "", sigType: "", rawBlob: nil,
	})

	sig, err := sshsig.Parse(armored)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	gx, gy := coordX(t, &signer.PublicKey), coordY(t, &signer.PublicKey)
	_ = gx

	keys := map[string]sshsig.PublicKey{
		"no point":      ecdsaKey(nil),
		"off the curve": ecdsaKey(rawKey(big.NewInt(1), big.NewInt(2))),
		"infinity":      ecdsaKey(rawKey(new(big.Int), new(big.Int))),
		"no X":          ecdsaKey(rawKey(nil, gy)),
	}

	for name, key := range keys {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := sig.Verify(key, sshsig.NamespaceStepUp, testMessage())
			if !errors.Is(err, sshsig.ErrMalformed) {
				t.Fatalf("Verify error = %v, want ErrMalformed", err)
			}

			if got != (sshsig.Result{UserPresent: false, UserVerified: false, Counter: 0}) {
				t.Fatalf("Verify result = %+v, want the zero result", got)
			}
		})
	}
}

// TestVerifyECDSALayers calls the ECDSA verifier alone. The signature types
// and the keys come from the caller, with none of the checks of Verify.
func TestVerifyECDSALayers(t *testing.T) {
	t.Parallel()

	signer := ecdsaTestKey(t)
	key := ecdsaKey(&signer.PublicKey)
	signed := []byte("signed data")
	sx, sy := coordX(t, &signer.PublicKey), coordY(t, &signer.PublicKey)
	n := elliptic.P256().Params().N
	one := big.NewInt(1)

	blob := ecdsaSign(t, signer, signed)

	blobOf := func(r, s *big.Int) []byte { return append(mpint(r), mpint(s)...) }

	tests := []struct {
		name string
		key  sshsig.PublicKey
		blob []byte
		want error
		text string // text that tells the layer; "" for none
	}{
		{"a valid signature", key, blob, nil, ""},
		{"no point", ecdsaKey(nil), blob, sshsig.ErrMalformed, "without a point"},
		{
			"no X",
			ecdsaKey(rawKey(nil, sy)),
			blob, sshsig.ErrMalformed, "not on the curve",
		},
		{
			"no Y",
			ecdsaKey(rawKey(sx, nil)),
			blob, sshsig.ErrMalformed, "not on the curve",
		},
		{
			"a point off the curve",
			ecdsaKey(rawKey(big.NewInt(1), big.NewInt(2))),
			blob, sshsig.ErrMalformed, "not on the curve",
		},
		{
			"the point at infinity",
			ecdsaKey(rawKey(new(big.Int), new(big.Int))),
			blob, sshsig.ErrMalformed, "not on the curve",
		},
		{"a byte after s", key, append(bytes.Clone(blob), 0), sshsig.ErrMalformed, ""},
		{"an empty blob", key, nil, sshsig.ErrMalformed, ""},
		{"a negative r", key, append(appendString(nil, []byte{0x80, 0x01}), mpint(one)...), sshsig.ErrMalformed, ""},
		{"r of zero", key, blobOf(new(big.Int), one), sshsig.ErrVerify, "out of range"},
		{"s of zero", key, blobOf(one, new(big.Int)), sshsig.ErrVerify, "out of range"},
		{"r equal to the order", key, blobOf(n, one), sshsig.ErrVerify, "out of range"},
		{"s equal to the order", key, blobOf(one, n), sshsig.ErrVerify, "out of range"},
		{"r above the order", key, blobOf(new(big.Int).Add(n, one), one), sshsig.ErrVerify, "out of range"},
		{"s above the order", key, blobOf(one, new(big.Int).Lsh(n, 1)), sshsig.ErrVerify, "out of range"},
		{"r of one and s of one", key, blobOf(one, one), sshsig.ErrVerify, "does not verify"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			sig := sshsig.Signature{
				PublicKey: nil, Namespace: "", HashAlgorithm: "", Type: sshsig.TypeECDSAP256,
				Blob: tc.blob, Flags: 0, Counter: 0,
			}

			got, err := sshsig.VerifyECDSA(sig, tc.key, signed)
			checkLayerError(t, err, tc.text, tc.want)

			if err != nil && got != (sshsig.Result{UserPresent: false, UserVerified: false, Counter: 0}) {
				t.Fatalf("result = %+v, want the zero result", got)
			}
		})
	}
}

// TestVerifyKeyECDSALayers calls the dispatch below the checks of Verify with
// a signature of a caller and an ECDSA key.
func TestVerifyKeyECDSALayers(t *testing.T) {
	t.Parallel()

	signer := ecdsaTestKey(t)
	key := ecdsaKey(&signer.PublicKey)

	tests := []struct {
		name  string
		key   sshsig.PublicKey
		flags byte
		count uint32
		want  error
	}{
		{"flags on an ECDSA signature", key, 0x01, 0, sshsig.ErrFlags},
		{"a counter on an ECDSA signature", key, 0, 1, sshsig.ErrFlags},
		{
			"a key type that has no branch",
			sshsig.PublicKey{Type: "ssh-rsa", Key: nil, Application: "", ECDSA: nil},
			0, 0, sshsig.ErrKeyType,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// The blob is a valid signature, so that only the layer under
			// test can refuse it.
			sig := sshsig.Signature{
				PublicKey: nil, Namespace: "", HashAlgorithm: sshsig.HashSHA512, Type: sshsig.TypeECDSAP256,
				Blob:  ecdsaSign(t, signer, signedFor(sshsig.NamespaceStepUp, sshsig.HashSHA512, testMessage())),
				Flags: tc.flags, Counter: tc.count,
			}

			digest := digestOf(sshsig.HashSHA512, testMessage())

			_, err := sshsig.VerifyKey(sig, tc.key, sshsig.NamespaceStepUp, digest)
			if !errors.Is(err, tc.want) {
				t.Fatalf("VerifyKey error = %v, want %v", err, tc.want)
			}
		})
	}
}

// TestVerifySignatureTypeLayer sends a Signature that a caller builds with
// another signature type to Verify. The parser refuses these types for the
// blob, so only a hand-built Signature reaches the check. The key match, the
// namespace and the hash are right, so that only the signature type check
// refuses it.
func TestVerifySignatureTypeLayer(t *testing.T) {
	t.Parallel()

	signer := ecdsaTestKey(t)

	parsed, err := sshsig.Parse(ecdsaArmored(t, signer, ecdsaOpts{
		namespace: sshsig.NamespaceStepUp, hash: "", sigType: "", rawBlob: nil,
	}))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	key := ecdsaKey(&signer.PublicKey)

	for _, sigType := range []string{sshsig.TypeEd25519, sshsig.TypeSKEd25519, "ecdsa-sha2-nistp384", ""} {
		t.Run("type "+sigType, func(t *testing.T) {
			t.Parallel()

			sig := parsed
			sig.Type = sigType

			_, err := sig.Verify(key, sshsig.NamespaceStepUp, testMessage())
			if !errors.Is(err, sshsig.ErrKeyType) {
				t.Fatalf("Verify error = %v, want ErrKeyType", err)
			}
		})
	}
}

// TestParsePublicKeyPointLayer sends hostile points to the parser. The key
// check that ParsePublicKey calls after the parser refuses a key with no
// point too, so the text of the error tells that the point parser refused it.
func TestParsePublicKeyPointLayer(t *testing.T) {
	t.Parallel()

	point, err := ecdsaTestKey(t).PublicKey.Bytes()
	if err != nil {
		t.Fatalf("encode test key: %v", err)
	}

	yPlusOne := bytes.Clone(point)
	yPlusOne[len(yPlusOne)-1]++

	points := map[string][]byte{
		"a point off the curve": yPlusOne,
		"the point at infinity": append([]byte{0x04}, make([]byte, 64)...),
		"a compressed point":    append([]byte{0x02}, point[1:33]...),
		"a point of 64 bytes":   point[:64],
		"an empty point":        nil,
	}

	for name, p := range points {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			wire := appendString(appendString(appendString(nil, []byte(sshsig.TypeECDSAP256)), []byte("nistp256")), p)

			_, err := sshsig.ParsePublicKey(wire)
			checkLayerError(t, err, "ECDSA point", sshsig.ErrMalformed)
		})
	}
}

// signedFor returns the data that the signer signs, for the hash algorithm
// and the message.
func signedFor(namespace, hash string, msg []byte) []byte {
	b := []byte("SSHSIG")
	b = appendString(b, []byte(namespace))
	b = appendString(b, nil)
	b = appendString(b, []byte(hash))

	return appendString(b, digestOf(hash, msg))
}

// checkLayerError checks that err wraps want, and that its text holds text.
// An empty text with a nil want means that err must be nil.
func checkLayerError(t *testing.T, err error, text string, want error) {
	t.Helper()

	if want == nil {
		if err != nil {
			t.Fatalf("error = %v, want none", err)
		}

		return
	}

	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}

	if !strings.Contains(err.Error(), text) {
		t.Fatalf("error = %q, want text %q", err, text)
	}
}

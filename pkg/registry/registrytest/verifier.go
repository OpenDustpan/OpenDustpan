package registrytest

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/registry"
)

// VerifierHarness is one verifier and the fixtures its cases need.
type VerifierHarness struct {
	Verifier registry.SignatureVerifier
	// Key is a trusted key. Sums and Signature are a checksums file and its
	// valid detached signature by Key.
	Key       registry.TrustedKey
	Sums      []byte
	Signature []byte
	// OtherKey is a valid key that did not sign Signature.
	OtherKey registry.TrustedKey
}

// VerifierFactory returns a fresh harness for one subtest.
type VerifierFactory func(t *testing.T) VerifierHarness

// RunVerifier runs the cases of the SignatureVerifier suite. The cases need
// only the fixtures of the harness. A case that needs special key material is
// a test of the adapter, because the way to make such a key depends on the
// library: an expired or revoked key, a signature by a subkey, a SHA-1
// signature, a DSA key and an RSA key of less than 2048 bits.
//
// A harness without a verifier or without fixtures skips the suite.
func RunVerifier(t *testing.T, newHarness VerifierFactory) {
	t.Helper()

	probe := newHarness(t)
	if probe.Verifier == nil || probe.Sums == nil {
		t.Skip("no SignatureVerifier adapter and no key fixture")
	}

	for _, tc := range verifierCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.test(t, newHarness(t))
		})
	}
}

type verifierCase struct {
	name string
	test func(t *testing.T, h VerifierHarness)
}

func verifierCases() []verifierCase {
	return []verifierCase{
		{"a valid signature returns the fingerprint of the key", testVerifyValid},
		{"a changed byte of the checksums file", testVerifyChangedSums},
		{"a changed byte of the signature", testVerifyChangedSignature},
		{"an empty signature and an empty checksums file", testVerifyEmpty},
		{"a signature by a key that is not trusted", testVerifyUntrusted},
		{"an empty key list", testVerifyNoKeys},
		{"a key that names another fingerprint", testVerifyWrongFingerprint},
		{"a canceled context", testVerifyCanceled},
	}
}

func testVerifyValid(t *testing.T, h VerifierHarness) {
	t.Helper()

	got, err := h.Verifier.Verify(t.Context(), h.Sums, h.Signature, []registry.TrustedKey{h.Key})
	if err != nil || got != h.Key.Fingerprint {
		t.Fatalf("Verify = %q, %v; want %q", got, err, h.Key.Fingerprint)
	}

	// The other key in the list changes nothing.
	got, err = h.Verifier.Verify(t.Context(), h.Sums, h.Signature, []registry.TrustedKey{h.OtherKey, h.Key})
	if err != nil || got != h.Key.Fingerprint {
		t.Fatalf("Verify with two keys = %q, %v; want %q", got, err, h.Key.Fingerprint)
	}
}

func testVerifyChangedSums(t *testing.T, h VerifierHarness) {
	t.Helper()

	for i := range h.Sums {
		sums := bytes.Clone(h.Sums)
		sums[i] ^= 0x01

		got, err := h.Verifier.Verify(t.Context(), sums, h.Signature, []registry.TrustedKey{h.Key})
		if !errors.Is(err, registry.ErrBadSignature) || got != "" {
			t.Fatalf("Verify with byte %d changed = %q, %v; want ErrBadSignature", i, got, err)
		}
	}

	// A line added at the end is a change too.
	got, err := h.Verifier.Verify(t.Context(), append(bytes.Clone(h.Sums), '\n'), h.Signature,
		[]registry.TrustedKey{h.Key})
	if !errors.Is(err, registry.ErrBadSignature) || got != "" {
		t.Fatalf("Verify with an added line = %q, %v; want ErrBadSignature", got, err)
	}
}

func testVerifyChangedSignature(t *testing.T, h VerifierHarness) {
	t.Helper()

	for i := range h.Signature {
		sig := bytes.Clone(h.Signature)
		sig[i] ^= 0x01

		got, err := h.Verifier.Verify(t.Context(), h.Sums, sig, []registry.TrustedKey{h.Key})
		if err == nil {
			// A byte in the part of the signature that no hash covers does not
			// change the signature of the checksums file. The result is then the
			// fingerprint of Key and no other.
			if got != h.Key.Fingerprint {
				t.Fatalf("Verify with signature byte %d changed = %q; want an error or %q", i, got, h.Key.Fingerprint)
			}

			continue
		}

		if got != "" || (!errors.Is(err, registry.ErrBadSignature) && !errors.Is(err, registry.ErrUntrustedKey)) {
			t.Fatalf("Verify with signature byte %d changed = %q, %v; want a registry sentinel", i, got, err)
		}
	}
}

func testVerifyEmpty(t *testing.T, h VerifierHarness) {
	t.Helper()

	keys := []registry.TrustedKey{h.Key}

	if got, err := h.Verifier.Verify(t.Context(), h.Sums, nil, keys); !errors.Is(err, registry.ErrBadSignature) || got != "" {
		t.Fatalf("Verify with no signature = %q, %v; want ErrBadSignature", got, err)
	}

	if got, err := h.Verifier.Verify(t.Context(), nil, h.Signature, keys); !errors.Is(err, registry.ErrBadSignature) || got != "" {
		t.Fatalf("Verify with no checksums file = %q, %v; want ErrBadSignature", got, err)
	}
}

func testVerifyUntrusted(t *testing.T, h VerifierHarness) {
	t.Helper()

	got, err := h.Verifier.Verify(t.Context(), h.Sums, h.Signature, []registry.TrustedKey{h.OtherKey})
	if !errors.Is(err, registry.ErrUntrustedKey) || got != "" {
		t.Fatalf("Verify = %q, %v; want ErrUntrustedKey", got, err)
	}
}

func testVerifyNoKeys(t *testing.T, h VerifierHarness) {
	t.Helper()

	got, err := h.Verifier.Verify(t.Context(), h.Sums, h.Signature, nil)
	if !errors.Is(err, registry.ErrUntrustedKey) || got != "" {
		t.Fatalf("Verify = %q, %v; want ErrUntrustedKey", got, err)
	}
}

// testVerifyWrongFingerprint holds the rule that the fingerprint of a trusted
// key is the fingerprint of its key material. A caller cannot name a key
// under the fingerprint of another key.
func testVerifyWrongFingerprint(t *testing.T, h VerifierHarness) {
	t.Helper()

	liar := registry.TrustedKey{Fingerprint: h.OtherKey.Fingerprint, Armored: h.Key.Armored}

	got, err := h.Verifier.Verify(t.Context(), h.Sums, h.Signature, []registry.TrustedKey{liar})
	if err == nil || got != "" {
		t.Fatalf("Verify = %q, %v; want an error and no fingerprint", got, err)
	}
}

func testVerifyCanceled(t *testing.T, h VerifierHarness) {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	got, err := h.Verifier.Verify(ctx, h.Sums, h.Signature, []registry.TrustedKey{h.Key})
	if err == nil || got != "" {
		t.Fatalf("Verify = %q, %v; want an error and no fingerprint", got, err)
	}
}

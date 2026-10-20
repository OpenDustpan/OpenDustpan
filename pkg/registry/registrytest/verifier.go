package registrytest

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/registry"
)

// VerifierHarness is one verifier and the fixtures its cases need. A fixture
// is nil until the key material exists.
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

// RunVerifier runs the cases of the SignatureVerifier suite. Each case skips
// until the harness gives its fixtures. The cases to fill in are:
//
//   - a valid signature returns the fingerprint of Key.
//   - a changed byte of Sums returns ErrBadSignature.
//   - a changed byte of Signature returns ErrBadSignature.
//   - a signature by OtherKey with only Key trusted returns ErrUntrustedKey.
//   - an empty key list returns ErrUntrustedKey.
//   - an expired or revoked key returns ErrBadSignature, checked at the time
//     of the call.
//   - a signature by a subkey returns the fingerprint of the primary key, and
//     the fingerprint is one of the fingerprints in the key list.
//   - a SHA-1 signature, a DSA key and an RSA key of less than 2048 bits each
//     return ErrBadSignature.
//   - a canceled context returns an error and never a fingerprint.
func RunVerifier(t *testing.T, newHarness VerifierFactory) {
	t.Helper()

	h := newHarness(t)
	if h.Verifier == nil || h.Sums == nil {
		t.Skip("no SignatureVerifier adapter and no key fixture yet")
	}

	t.Run("valid signature", func(t *testing.T) {
		t.Parallel()

		got, err := h.Verifier.Verify(t.Context(), h.Sums, h.Signature, []registry.TrustedKey{h.Key})
		if err != nil || got != h.Key.Fingerprint {
			t.Fatalf("Verify = %q, %v; want %q", got, err, h.Key.Fingerprint)
		}
	})
}

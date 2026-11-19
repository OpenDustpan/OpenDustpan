package trustrulesvc

import (
	"crypto/x509"
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/trustrule"
)

// ForgeChain returns a VerifiedChain with the values that the caller gives, to
// test the checks of Exchange against a value that VerifyChain never makes.
func ForgeChain(leaf *x509.Certificate, spiffeID string, bundle trustrule.BundleID, version int64) VerifiedChain {
	return VerifiedChain{
		leaf: leaf, spiffeID: spiffeID, leafNotAfter: leaf.NotAfter.UTC(), bundleID: bundle, bundleVersion: version,
	}
}

// TestMtlsCheckLeafTime sends leaves outside their validity time to the time
// check, as if x509 had not checked them.
func TestMtlsCheckLeafTime(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_700_000_000, 0).UTC()

	for name, tc := range map[string]struct {
		notBefore, notAfter time.Time
		want                error
	}{
		"valid":         {now.Add(-time.Second), now.Add(time.Second), nil},
		"just expired":  {now.Add(-time.Hour), now.Add(-time.Nanosecond), ErrExpired},
		"not yet valid": {now.Add(time.Nanosecond), now.Add(time.Hour), ErrNotYetValid},
	} {
		leaf := new(x509.Certificate)
		leaf.NotBefore, leaf.NotAfter = tc.notBefore, tc.notAfter

		if err := checkLeafTime(leaf, now); !errors.Is(err, tc.want) {
			t.Errorf("%s: error = %v, want %v", name, err, tc.want)
		}
	}
}

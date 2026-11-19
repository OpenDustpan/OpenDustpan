package trustrulesvc

import (
	"crypto/x509"
	"errors"
	"net/url"
	"strings"
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

// TestMtlsLeafSPIFFEIDWildcard sends a leaf with a * in its SPIFFE ID to
// leafSPIFFEID, as if the pattern check after it did not refuse the *.
func TestMtlsLeafSPIFFEIDWildcard(t *testing.T) {
	t.Parallel()

	id, err := url.Parse("spiffe://example.org/ns/*/sa/x")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	leaf := new(x509.Certificate)
	leaf.URIs = []*url.URL{id}

	_, err = leafSPIFFEID(leaf)
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "holds a *") ||
		strings.Contains(err.Error(), "not a SPIFFE ID") {
		t.Fatalf("error = %v, want ErrInvalid from the * check, not from the pattern check", err)
	}
}

// TestMtlsActiveRoots sends bundles that Verify never reads from the store to
// activeRoots.
func TestMtlsActiveRoots(t *testing.T) {
	t.Parallel()

	ca := new(x509.Certificate)
	ca.Raw = []byte{1}
	versions := func(n int64) []trustrule.BundleVersion {
		return []trustrule.BundleVersion{{
			Number: n, Certificates: []*x509.Certificate{ca}, CreatedAt: time.Time{}, OverlapEndsAt: time.Time{},
		}}
	}

	bundle := func(id trustrule.BundleID, active, number int64) trustrule.Bundle {
		return trustrule.Bundle{
			ID: id, Namespace: "acme", Active: active, Versions: versions(number), Revision: 1, CreatedAt: time.Time{},
		}
	}

	empty := bundle("trb-one", 1, 1)
	empty.Versions[0].Certificates = nil

	for name, b := range map[string]trustrule.Bundle{
		"an active version that holds no certificate":                 empty,
		"no active version, and a version numbered 0 that holds a CA": bundle("trb-one", 0, 0),
		"a bundle ID that is not valid":                               bundle("bad", 1, 1),
	} {
		if _, _, err := activeRoots(&b); !errors.Is(err, ErrBundleUnknown) {
			t.Errorf("%s: error = %v, want ErrBundleUnknown", name, err)
		}
	}

	good := bundle("trb-one", 1, 1)
	if _, n, err := activeRoots(&good); err != nil || n != 1 {
		t.Errorf("good bundle: version = %d, error = %v", n, err)
	}
}

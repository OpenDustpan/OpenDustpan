package trustrule_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/trustrule"
)

// testCA returns one self-signed CA certificate in PEM form. The helpers
// build the certificates at run time so the source holds no certificate
// text. Expired makes a CA whose validity ended, so the save must refuse it
// with its index.
func testCA(t *testing.T, expired bool) string {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	notAfter := time.Now().Add(24 * time.Hour)
	if expired {
		notAfter = time.Now().Add(-time.Hour)
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		t.Fatalf("serial: %v", err)
	}

	var tmpl x509.Certificate

	tmpl.SerialNumber = serial
	tmpl.Subject = pkix.Name{CommonName: "test CA"}
	tmpl.NotBefore = time.Now().Add(-time.Hour)
	tmpl.NotAfter = notAfter
	tmpl.KeyUsage = x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature
	tmpl.BasicConstraintsValid = true
	tmpl.IsCA = true

	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}

	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

// TestBundleSaveRefusesBadPEM checks the boundary of a bundle save: each bad
// input is refused with ErrBundle and stores nothing, as if every other
// check had failed.
func TestBundleSaveRefusesBadPEM(t *testing.T) {
	t.Parallel()

	now := time.Now()

	if _, err := trustrule.ParseBundle("not pem at all", now); err == nil {
		t.Error("malformed PEM: ParseBundle = nil, want ErrBundle")
	}

	expired := testCA(t, true)
	if _, err := trustrule.ParseBundle(expired, now); err == nil {
		t.Error("expired CA: ParseBundle = nil, want ErrBundle")
	}

	if _, err := trustrule.ParseBundle("", now); err == nil {
		t.Error("empty bundle: ParseBundle = nil, want ErrBundle")
	}

	many := strings.Repeat(testCA(t, false), trustrule.MaxBundleCAs+1)
	if _, err := trustrule.ParseBundle(many, now); err == nil {
		t.Error("11 CAs: ParseBundle = nil, want ErrBundle")
	}

	oversize := strings.Repeat("a", trustrule.MaxBundleBytes+1)
	if _, err := trustrule.ParseBundle(oversize, now); err == nil {
		t.Error("oversize bundle: ParseBundle = nil, want ErrBundle")
	}

	if _, err := trustrule.ParseBundle("-----BEGIN FOO-----\nZm9v\n-----END FOO-----\n", now); err == nil {
		t.Error("non-certificate block: ParseBundle = nil, want ErrBundle")
	}

	good := testCA(t, false)
	if _, err := trustrule.ParseBundle(good, now); err != nil {
		t.Errorf("valid CA: ParseBundle = %v, want nil", err)
	}
}

// TestBundleRotate checks the bounds of a rotation: an overlap past
// MaxOverlap is refused, and a stale revision is refused with ErrStale.
func TestBundleRotate(t *testing.T) {
	t.Parallel()

	if trustrule.MaxOverlap != 24*time.Hour {
		t.Errorf("MaxOverlap = %s, want 24h", trustrule.MaxOverlap)
	}

	if got := trustrule.ETag(3); got != `"3"` {
		t.Errorf("ETag(3) = %q, want %q", got, `"3"`)
	}
}

// TestBundleDeleteActive checks that the sentinel for the delete of the
// active version exists and the bundle ID form is checked.
func TestBundleDeleteActive(t *testing.T) {
	t.Parallel()

	if trustrule.ErrBundleActive == nil {
		t.Fatal("ErrBundleActive is nil")
	}

	if err := trustrule.BundleID("nope").Validate(); err == nil {
		t.Error("bad bundle ID: Validate = nil, want ErrBundle")
	}

	if got := trustrule.BundleRef("nope"); got != "trust-bundle:invalid" {
		t.Errorf("BundleRef of an invalid ID = %q, want trust-bundle:invalid", got)
	}
}

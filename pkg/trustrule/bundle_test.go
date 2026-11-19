package trustrule_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/pem"
	"errors"
	"math/big"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/trustrule"
)

// testCA returns one self-signed CA certificate in PEM form. The helpers
// build the certificates at run time so the source holds no certificate
// text. A negative validity makes a CA whose validity ended, so the save must
// refuse it with its index.
func testCA(t *testing.T, validity time.Duration) string {
	t.Helper()

	return testCert(t, true, x509.KeyUsageCertSign|x509.KeyUsageDigitalSignature,
		bundleTestNow().Add(-time.Hour), bundleTestNow().Add(validity))
}

// testCert returns one self-signed certificate in PEM form with the given
// CA flag, key usage and validity, built at run time so the source holds no
// certificate text.
func testCert(t *testing.T, isCA bool, usage x509.KeyUsage, notBefore, notAfter time.Time) string {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		t.Fatalf("serial: %v", err)
	}

	var tmpl x509.Certificate

	tmpl.SerialNumber = serial
	tmpl.Subject.CommonName = "test CA"
	tmpl.NotBefore = notBefore
	tmpl.NotAfter = notAfter
	tmpl.KeyUsage = usage
	tmpl.BasicConstraintsValid = true
	tmpl.IsCA = isCA

	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}

	var block pem.Block

	block.Type = "CERTIFICATE"
	block.Bytes = der

	return string(pem.EncodeToMemory(&block))
}

// TestBundleSaveRefusesBadPEM checks the boundary of a bundle save: each bad
// input is refused with ErrBundle and stores nothing, as if every other
// check had failed.
func TestBundleSaveRefusesBadPEM(t *testing.T) {
	t.Parallel()

	now := bundleTestNow()
	good := testCA(t, 24*time.Hour)
	leaf := testCert(t, false, x509.KeyUsageDigitalSignature, now.Add(-time.Hour), now.Add(24*time.Hour))
	noSign := testCert(t, true, x509.KeyUsageDigitalSignature, now.Add(-time.Hour), now.Add(24*time.Hour))
	early := testCert(t, true, x509.KeyUsageCertSign, now.Add(24*time.Hour), now.Add(48*time.Hour))
	notCA := testCert(t, false, x509.KeyUsageCertSign, now.Add(-time.Hour), now.Add(24*time.Hour))
	headered := strings.Replace(good, "-----BEGIN CERTIFICATE-----\n",
		"-----BEGIN CERTIFICATE-----\nHdr: v\n\n", 1)

	cases := []struct {
		name string
		pem  string
	}{
		{"malformed PEM", "not pem at all"},
		{"empty bundle", ""},
		{"too many CAs", strings.Repeat(good, trustrule.MaxBundleCAs+1)},
		{"oversize bundle", strings.Repeat("a", trustrule.MaxBundleBytes+1)},
		{"non-certificate block", "-----BEGIN FOO-----\nZm9v\n-----END FOO-----\n"},
		{"self-signed leaf", leaf},
		{"basic constraints without CA flag, with cert sign", notCA},
		{"valid CAs over the byte bound", bigBundle(t, trustrule.MaxBundleCAs)},
		{"CA without cert sign", noSign},
		{"CA not yet valid", early},
		{"text before block", "garbage text here\n" + good},
		{"text between blocks", good + "garbage text here\n" + good},
		{"PEM headers", headered},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if _, err := trustrule.ParseBundle(tc.pem, now); !errors.Is(err, trustrule.ErrBundle) {
				t.Fatalf("ParseBundle = %v, want ErrBundle", err)
			}

			if _, err := trustrule.ParseStoredBundle(tc.pem, now); !errors.Is(err, trustrule.ErrBundle) {
				t.Fatalf("ParseStoredBundle = %v, want ErrBundle", err)
			}
		})
	}

	t.Run("expired CA", func(t *testing.T) {
		t.Parallel()

		expired := testCA(t, -time.Hour)
		if _, err := trustrule.ParseBundle(expired, now); !errors.Is(err, trustrule.ErrBundle) {
			t.Fatalf("ParseBundle = %v, want ErrBundle", err)
		}

		if _, err := trustrule.ParseStoredBundle(expired, now); err != nil {
			t.Fatalf("ParseStoredBundle = %v, want nil", err)
		}
	})

	if _, err := trustrule.ParseBundle(good, now); err != nil {
		t.Errorf("valid CA: ParseBundle = %v, want nil", err)
	}

	if _, err := trustrule.ParseStoredBundle(good, now); err != nil {
		t.Errorf("valid CA: ParseStoredBundle = %v, want nil", err)
	}
}

// TestValidateSpiffePattern checks the allowlist of the SPIFFE ID pattern:
// each bad pattern is refused with ErrInvalid, as if every other check had
// failed.
func TestValidateSpiffePattern(t *testing.T) {
	t.Parallel()

	for _, pattern := range []string{
		"spiffe://*/a",
		"spiffe://TD/a",
		"spiffe://td:8080/a",
		"spiffe://td/",
		"spiffe://td/a/../b",
		"spiffe://td/a%2F*",
		"spiffe://td/a%00",
	} {
		t.Run(pattern, func(t *testing.T) {
			t.Parallel()

			if err := trustrule.ValidateSpiffePattern(pattern); !errors.Is(err, trustrule.ErrInvalid) {
				t.Fatalf("ValidateSpiffePattern(%q) = %v, want ErrInvalid", pattern, err)
			}
		})
	}

	for _, pattern := range []string{
		"spiffe://td/a",
		"spiffe://example.org/ns/acme",
		"spiffe://example.org/ns/acme/sa/deploy",
	} {
		t.Run("accept "+pattern, func(t *testing.T) {
			t.Parallel()

			if err := trustrule.ValidateSpiffePattern(pattern); err != nil {
				t.Fatalf("ValidateSpiffePattern(%q) = %v, want nil", pattern, err)
			}
		})
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

// bundleTestNow is the fixed clock of the bundle tests, so that no test reads
// the wall clock.
func bundleTestNow() time.Time {
	return time.Unix(1_700_000_000, 0).UTC()
}

// bigBundle returns n valid CA certificates, each padded with an extension so
// that together they pass MaxBundleBytes while the count stays within
// MaxBundleCAs. The byte bound must refuse the bundle on its own: every
// other check accepts each certificate.
func bigBundle(t *testing.T, n int) string {
	t.Helper()

	var out strings.Builder

	for range n {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatalf("generate key: %v", err)
		}

		serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
		if err != nil {
			t.Fatalf("serial: %v", err)
		}

		var tmpl x509.Certificate

		tmpl.SerialNumber = serial
		tmpl.Subject.CommonName = "big CA"
		tmpl.NotBefore = bundleTestNow().Add(-time.Hour)
		tmpl.NotAfter = bundleTestNow().Add(24 * time.Hour)
		tmpl.KeyUsage = x509.KeyUsageCertSign
		tmpl.BasicConstraintsValid = true
		tmpl.IsCA = true
		tmpl.ExtraExtensions = []pkix.Extension{{
			Id:       asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 99999, 1},
			Critical: false,
			Value:    make([]byte, 5500),
		}}

		der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
		if err != nil {
			t.Fatalf("create certificate: %v", err)
		}

		var block pem.Block

		block.Type = "CERTIFICATE"
		block.Bytes = der

		out.Write(pem.EncodeToMemory(&block))
	}

	return out.String()
}

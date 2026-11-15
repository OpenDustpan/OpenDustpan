package trustruletest

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/trustrule"
)

// bundleCases are the cases of the trust bundle of a namespace.
func bundleCases() []suiteCase {
	return []suiteCase{
		{"SaveBundleMakesTheBundleWithTheFirstVersionActive", testSaveFirst},
		{"SaveBundleAddsAVersionAndKeepsTheActivePointer", testSaveSecond},
		{"SaveBundleRefusesInputThatFailsTheChecks", testSaveRefuses},
		{"SaveBundleRefusesAnOverlap", testSaveOverlap},
		{"SaveBundleKeepsAtMostTheMaximumOfVersions", testSaveRetires},
		{"SaveBundleRefusesAStaleFence", testSaveStaleFence},
		{"SaveBundleRefusesTheFenceOfAnotherNamespace", testSaveOtherFence},
		{"SaveBundleRefusesAnEntryOfAnotherNamespace", testSaveOtherEntry},
		{"GetBundleOfANamespaceWithNoBundleIsNotFound", testGetBundleNone},
		{"GetBundleServesOnlyTheBundleOfItsNamespace", testGetBundleOtherNamespace},
		{"RotateBundleMovesTheActivePointerAndStatesTheOverlap", testRotate},
		{"RotateBundleRefusesAStaleRevision", testRotateStale},
		{"RotateBundleRefusesAnOverlapOverTheMaximum", testRotateOverlap},
		{"RotateBundleRefusesInputThatFailsTheChecks", testRotateBadPEM},
		{"RotateBundleOfANamespaceWithNoBundleIsNotFound", testRotateNone},
		{"RotateBundleRefusesAStaleFence", testRotateStaleFence},
		{"DeleteBundleVersionRemovesARetiredVersion", testDeleteVersion},
		{"DeleteBundleVersionRefusesTheActiveVersion", testDeleteActive},
		{"DeleteBundleVersionOfAnUnknownVersionIsNotFound", testDeleteUnknownVersion},
		{"DeleteBundleVersionRefusesAStaleFence", testDeleteVersionStaleFence},
	}
}

// bundleNow returns the time of the entries of the cases. A real time keeps
// the test CAs unexpired.
func bundleNow() time.Time {
	return time.Now().UTC().Truncate(time.Millisecond)
}

// bundleEntry returns a valid audit entry for a bundle write in ns.
func bundleEntry(ns namespace.Name, action string) audit.Entry {
	e := auditEntry(ns, action, "")
	e.Action = "trust-bundle:" + action
	e.At = bundleNow()

	return e
}

// bundleEntries returns the audit entries of bundle changes of ns.
func bundleEntries(t *testing.T, h Harness, ns string) []audit.Entry {
	t.Helper()

	var out []audit.Entry

	entries := h.Audit(t, ns)

	for i := range entries {
		if strings.HasPrefix(entries[i].Action, "trust-bundle:") {
			out = append(out, entries[i])
		}
	}

	return out
}

// testCA returns the PEM text of one new CA certificate, expired or valid.
func testCA(t *testing.T, expired bool) string {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	serial, err := rand.Int(rand.Reader, big.NewInt(1<<62))
	if err != nil {
		t.Fatalf("serial: %v", err)
	}

	notBefore, notAfter := time.Now().Add(-time.Hour), time.Now().Add(48*time.Hour)
	if expired {
		notBefore, notAfter = time.Now().Add(-48*time.Hour), time.Now().Add(-time.Hour)
	}

	tmpl := &x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: "test ca"}, NotBefore: notBefore, NotAfter: notAfter,
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}

	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

// saveBundle saves a new CA as a version of the bundle of acme.
func saveBundle(t *testing.T, h Harness) trustrule.Bundle {
	t.Helper()

	fence := h.Namespace(t, nsAcme)

	b, err := h.Bundles.SaveBundle(t.Context(), nsAcme, trustrule.BundleFields{PEM: testCA(t, false)}, fence,
		bundleEntry(nsAcme, "save"))
	if err != nil {
		t.Fatalf("save bundle: %v", err)
	}

	return b
}

// mustGetBundle reads the bundle of acme.
func mustGetBundle(t *testing.T, h Harness) trustrule.Bundle {
	t.Helper()

	b, err := h.Bundles.GetBundle(t.Context(), nsAcme)
	if err != nil {
		t.Fatalf("get bundle: %v", err)
	}

	return b
}

func testSaveFirst(t *testing.T, h Harness) {
	t.Helper()

	b := saveBundle(t, h)

	if b.Namespace != nsAcme || b.Active != 1 || b.Revision != 1 || len(b.Versions) != 1 ||
		b.Versions[0].Number != 1 || len(b.Versions[0].Certificates) != 1 {
		t.Fatalf("bundle = %+v, want one version, active 1, revision 1", b)
	}

	if err := b.ID.Validate(); err != nil {
		t.Errorf("bundle ID: %v", err)
	}

	if !b.Versions[0].OverlapEndsAt.IsZero() {
		t.Errorf("overlap end of the first version = %v, want zero", b.Versions[0].OverlapEndsAt)
	}

	got := mustGetBundle(t, h)
	if got.ID != b.ID || got.Active != 1 || got.Revision != 1 || len(got.Versions) != 1 {
		t.Errorf("get = %+v, want %+v", got, b)
	}

	entries := bundleEntries(t, h, nsAcme)
	if len(entries) != 1 || entries[0].Action != "trust-bundle:save" || entries[0].ObjectID != string(b.ID) {
		t.Errorf("audit entries = %+v, want one trust-bundle:save of %s", entries, b.ID)
	}
}

func testSaveSecond(t *testing.T, h Harness) {
	t.Helper()

	first := saveBundle(t, h)
	fence := h.Namespace(t, nsAcme)

	b, err := h.Bundles.SaveBundle(t.Context(), nsAcme, trustrule.BundleFields{PEM: testCA(t, false)}, fence,
		bundleEntry(nsAcme, "save"))
	if err != nil {
		t.Fatalf("second save: %v", err)
	}

	if b.ID != first.ID || b.Active != 1 || b.Revision != 2 || len(b.Versions) != 2 || b.Versions[1].Number != 2 {
		t.Fatalf("bundle = %+v, want the same bundle, active 1, revision 2, versions 1 and 2", b)
	}
}

func testSaveRefuses(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, nsAcme)

	cases := map[string]string{
		"empty":            "",
		"not PEM":          "not pem at all",
		"expired CA":       testCA(t, true),
		"not a CA block":   "-----BEGIN PRIVATE KEY-----\nZm9v\n-----END PRIVATE KEY-----\n",
		"bad certificate":  "-----BEGIN CERTIFICATE-----\nZm9v\n-----END CERTIFICATE-----\n",
		"11 CAs":           strings.Repeat(testCA(t, false), trustrule.MaxBundleCAs+1),
		"oversize":         strings.Repeat("a", trustrule.MaxBundleBytes+1),
		"good and expired": testCA(t, false) + testCA(t, true),
	}

	for name, text := range cases {
		_, err := h.Bundles.SaveBundle(t.Context(), nsAcme, trustrule.BundleFields{PEM: text}, fence,
			bundleEntry(nsAcme, "save"))
		if !errors.Is(err, trustrule.ErrBundle) {
			t.Errorf("save of %s = %v, want ErrBundle", name, err)
		}
	}

	if _, err := h.Bundles.GetBundle(t.Context(), nsAcme); !errors.Is(err, trustrule.ErrNotFound) {
		t.Errorf("get after refused saves = %v, want ErrNotFound", err)
	}

	if got := len(bundleEntries(t, h, nsAcme)); got != 0 {
		t.Errorf("audit entries = %d, want 0", got)
	}
}

func testSaveOverlap(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, nsAcme)

	_, err := h.Bundles.SaveBundle(t.Context(), nsAcme,
		trustrule.BundleFields{PEM: testCA(t, false), Overlap: time.Hour}, fence, bundleEntry(nsAcme, "save"))
	if !errors.Is(err, trustrule.ErrBundle) {
		t.Fatalf("save with an overlap = %v, want ErrBundle", err)
	}
}

func testSaveRetires(t *testing.T, h Harness) {
	t.Helper()

	first := saveBundle(t, h)
	fence := h.Namespace(t, nsAcme)

	for range trustrule.MaxBundleVersions + 2 {
		if _, err := h.Bundles.SaveBundle(t.Context(), nsAcme, trustrule.BundleFields{PEM: testCA(t, false)}, fence,
			bundleEntry(nsAcme, "save")); err != nil {
			t.Fatalf("save: %v", err)
		}
	}

	b := mustGetBundle(t, h)
	if len(b.Versions) != trustrule.MaxBundleVersions {
		t.Fatalf("versions = %d, want %d", len(b.Versions), trustrule.MaxBundleVersions)
	}

	// The active version stays, whatever its age.
	if b.ID != first.ID || b.Active != 1 || b.Versions[0].Number != 1 {
		t.Errorf("bundle = active %d, first version %d, want the active version 1 kept", b.Active,
			b.Versions[0].Number)
	}

	if last := b.Versions[len(b.Versions)-1].Number; last != int64(trustrule.MaxBundleVersions+3) {
		t.Errorf("newest version = %d, want %d", last, trustrule.MaxBundleVersions+3)
	}
}

func testSaveStaleFence(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, nsAcme)

	_, err := h.Bundles.SaveBundle(t.Context(), nsAcme, trustrule.BundleFields{PEM: testCA(t, false)},
		staleFence(fence), bundleEntry(nsAcme, "save"))
	if !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("save with a stale fence = %v, want ErrFenced", err)
	}

	if _, err := h.Bundles.GetBundle(t.Context(), nsAcme); !errors.Is(err, trustrule.ErrNotFound) {
		t.Errorf("get = %v, want ErrNotFound", err)
	}
}

func testSaveOtherFence(t *testing.T, h Harness) {
	t.Helper()

	h.Namespace(t, nsAcme)
	other := h.Namespace(t, nsOther)

	_, err := h.Bundles.SaveBundle(t.Context(), nsAcme, trustrule.BundleFields{PEM: testCA(t, false)}, other,
		bundleEntry(nsOther, "save"))
	if !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("save with the fence of another namespace = %v, want ErrFenced", err)
	}

	if _, err := h.Bundles.GetBundle(t.Context(), nsAcme); !errors.Is(err, trustrule.ErrNotFound) {
		t.Errorf("get = %v, want ErrNotFound", err)
	}
}

func testSaveOtherEntry(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, nsAcme)
	h.Namespace(t, nsOther)

	_, err := h.Bundles.SaveBundle(t.Context(), nsAcme, trustrule.BundleFields{PEM: testCA(t, false)}, fence,
		bundleEntry(nsOther, "save"))
	if !errors.Is(err, audit.ErrInvalidEntry) {
		t.Fatalf("save with an entry of another namespace = %v, want audit.ErrInvalidEntry", err)
	}

	if _, err := h.Bundles.GetBundle(t.Context(), nsAcme); !errors.Is(err, trustrule.ErrNotFound) {
		t.Errorf("get = %v, want ErrNotFound", err)
	}
}

func testGetBundleNone(t *testing.T, h Harness) {
	t.Helper()

	h.Namespace(t, nsAcme)

	if _, err := h.Bundles.GetBundle(t.Context(), nsAcme); !errors.Is(err, trustrule.ErrNotFound) {
		t.Errorf("get = %v, want ErrNotFound", err)
	}

	if _, err := h.Bundles.GetBundle(t.Context(), "Not A Namespace!"); !errors.Is(err, trustrule.ErrInvalid) {
		t.Errorf("get of a bad namespace = %v, want ErrInvalid", err)
	}
}

func testGetBundleOtherNamespace(t *testing.T, h Harness) {
	t.Helper()

	saveBundle(t, h)
	h.Namespace(t, nsOther)

	if _, err := h.Bundles.GetBundle(t.Context(), nsOther); !errors.Is(err, trustrule.ErrNotFound) {
		t.Errorf("get of another namespace = %v, want ErrNotFound", err)
	}
}

func testRotate(t *testing.T, h Harness) {
	t.Helper()

	first := saveBundle(t, h)
	fence := h.Namespace(t, nsAcme)

	entry := bundleEntry(nsAcme, "rotate")
	overlap := 2 * time.Hour

	b, err := h.Bundles.RotateBundle(t.Context(), nsAcme,
		trustrule.BundleFields{PEM: testCA(t, false), Overlap: overlap}, first.Revision, fence, entry)
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}

	if b.ID != first.ID || b.Active != 2 || b.Revision != 2 || len(b.Versions) != 2 {
		t.Fatalf("bundle = %+v, want active 2, revision 2, two versions", b)
	}

	want := entry.At.Add(overlap)
	if got := b.Versions[1].OverlapEndsAt; !got.Equal(want) {
		t.Errorf("overlap ends at %v, want %v", got, want)
	}

	got := mustGetBundle(t, h)
	if got.Active != 2 || got.Revision != 2 || !got.Versions[1].OverlapEndsAt.Equal(want) {
		t.Errorf("get = %+v, want the rotated bundle", got)
	}

	entries := bundleEntries(t, h, nsAcme)
	if len(entries) != 2 || entries[1].Action != "trust-bundle:rotate" {
		t.Errorf("audit entries = %+v, want the save and then the rotate", entries)
	}
}

func testRotateStale(t *testing.T, h Harness) {
	t.Helper()

	first := saveBundle(t, h)
	fence := h.Namespace(t, nsAcme)

	for _, revision := range []int64{0, 2, first.Revision + 5} {
		_, err := h.Bundles.RotateBundle(t.Context(), nsAcme, trustrule.BundleFields{PEM: testCA(t, false)},
			revision, fence, bundleEntry(nsAcme, "rotate"))
		if !errors.Is(err, trustrule.ErrStale) {
			t.Errorf("rotate with revision %d = %v, want ErrStale", revision, err)
		}
	}

	if got := mustGetBundle(t, h); got.Active != 1 || got.Revision != 1 || len(got.Versions) != 1 {
		t.Errorf("bundle = %+v, want it unchanged", got)
	}
}

func testRotateOverlap(t *testing.T, h Harness) {
	t.Helper()

	first := saveBundle(t, h)
	fence := h.Namespace(t, nsAcme)

	for _, overlap := range []time.Duration{trustrule.MaxOverlap + time.Second, -time.Second} {
		_, err := h.Bundles.RotateBundle(t.Context(), nsAcme,
			trustrule.BundleFields{PEM: testCA(t, false), Overlap: overlap}, first.Revision, fence,
			bundleEntry(nsAcme, "rotate"))
		if !errors.Is(err, trustrule.ErrBundle) {
			t.Errorf("rotate with overlap %v = %v, want ErrBundle", overlap, err)
		}
	}

	// The maximum itself is allowed.
	if _, err := h.Bundles.RotateBundle(t.Context(), nsAcme,
		trustrule.BundleFields{PEM: testCA(t, false), Overlap: trustrule.MaxOverlap}, first.Revision, fence,
		bundleEntry(nsAcme, "rotate")); err != nil {
		t.Errorf("rotate with the maximum overlap: %v", err)
	}
}

func testRotateBadPEM(t *testing.T, h Harness) {
	t.Helper()

	first := saveBundle(t, h)
	fence := h.Namespace(t, nsAcme)

	for name, text := range map[string]string{"expired": testCA(t, true), "not PEM": "x", "empty": ""} {
		_, err := h.Bundles.RotateBundle(t.Context(), nsAcme, trustrule.BundleFields{PEM: text}, first.Revision,
			fence, bundleEntry(nsAcme, "rotate"))
		if !errors.Is(err, trustrule.ErrBundle) {
			t.Errorf("rotate with %s = %v, want ErrBundle", name, err)
		}
	}

	if got := mustGetBundle(t, h); got.Active != 1 || len(got.Versions) != 1 {
		t.Errorf("bundle = %+v, want it unchanged", got)
	}
}

func testRotateNone(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, nsAcme)

	_, err := h.Bundles.RotateBundle(t.Context(), nsAcme, trustrule.BundleFields{PEM: testCA(t, false)}, 1, fence,
		bundleEntry(nsAcme, "rotate"))
	if !errors.Is(err, trustrule.ErrNotFound) {
		t.Fatalf("rotate with no bundle = %v, want ErrNotFound", err)
	}
}

func testRotateStaleFence(t *testing.T, h Harness) {
	t.Helper()

	first := saveBundle(t, h)
	fence := h.Namespace(t, nsAcme)

	_, err := h.Bundles.RotateBundle(t.Context(), nsAcme, trustrule.BundleFields{PEM: testCA(t, false)},
		first.Revision, staleFence(fence), bundleEntry(nsAcme, "rotate"))
	if !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("rotate with a stale fence = %v, want ErrFenced", err)
	}

	if got := mustGetBundle(t, h); got.Active != 1 || len(got.Versions) != 1 {
		t.Errorf("bundle = %+v, want it unchanged", got)
	}
}

func testDeleteVersion(t *testing.T, h Harness) {
	t.Helper()

	first := saveBundle(t, h)
	fence := h.Namespace(t, nsAcme)

	// Rotate, so that version 1 is retired.
	if _, err := h.Bundles.RotateBundle(t.Context(), nsAcme, trustrule.BundleFields{PEM: testCA(t, false)},
		first.Revision, fence, bundleEntry(nsAcme, "rotate")); err != nil {
		t.Fatalf("rotate: %v", err)
	}

	if err := h.Bundles.DeleteBundleVersion(t.Context(), nsAcme, 1, fence, bundleEntry(nsAcme, "delete")); err != nil {
		t.Fatalf("delete version 1: %v", err)
	}

	b := mustGetBundle(t, h)
	if len(b.Versions) != 1 || b.Versions[0].Number != 2 || b.Active != 2 {
		t.Errorf("bundle = %+v, want version 2 only", b)
	}

	entries := bundleEntries(t, h, nsAcme)
	if got := entries[len(entries)-1].Action; got != "trust-bundle:delete" {
		t.Errorf("last audit action = %s, want trust-bundle:delete", got)
	}

	// A repeated delete finds no version.
	err := h.Bundles.DeleteBundleVersion(t.Context(), nsAcme, 1, fence, bundleEntry(nsAcme, "delete"))
	if !errors.Is(err, trustrule.ErrNotFound) {
		t.Errorf("repeated delete = %v, want ErrNotFound", err)
	}
}

func testDeleteActive(t *testing.T, h Harness) {
	t.Helper()

	saveBundle(t, h)
	fence := h.Namespace(t, nsAcme)

	err := h.Bundles.DeleteBundleVersion(t.Context(), nsAcme, 1, fence, bundleEntry(nsAcme, "delete"))
	if !errors.Is(err, trustrule.ErrBundleActive) {
		t.Fatalf("delete of the active version = %v, want ErrBundleActive", err)
	}

	if got := mustGetBundle(t, h); len(got.Versions) != 1 {
		t.Errorf("versions = %d, want 1", len(got.Versions))
	}

	if got := len(bundleEntries(t, h, nsAcme)); got != 1 {
		t.Errorf("audit entries = %d, want 1", got)
	}
}

func testDeleteUnknownVersion(t *testing.T, h Harness) {
	t.Helper()

	saveBundle(t, h)
	fence := h.Namespace(t, nsAcme)

	for _, version := range []int64{0, -1, 9} {
		err := h.Bundles.DeleteBundleVersion(t.Context(), nsAcme, version, fence, bundleEntry(nsAcme, "delete"))
		if !errors.Is(err, trustrule.ErrNotFound) {
			t.Errorf("delete of version %d = %v, want ErrNotFound", version, err)
		}
	}

	otherFence := h.Namespace(t, nsOther)

	err := h.Bundles.DeleteBundleVersion(t.Context(), nsOther, 1, otherFence, bundleEntry(nsOther, "delete"))
	if !errors.Is(err, trustrule.ErrNotFound) {
		t.Errorf("delete in a namespace with no bundle = %v, want ErrNotFound", err)
	}
}

func testDeleteVersionStaleFence(t *testing.T, h Harness) {
	t.Helper()

	first := saveBundle(t, h)
	fence := h.Namespace(t, nsAcme)

	if _, err := h.Bundles.RotateBundle(t.Context(), nsAcme, trustrule.BundleFields{PEM: testCA(t, false)},
		first.Revision, fence, bundleEntry(nsAcme, "rotate")); err != nil {
		t.Fatalf("rotate: %v", err)
	}

	err := h.Bundles.DeleteBundleVersion(t.Context(), nsAcme, 1, staleFence(fence), bundleEntry(nsAcme, "delete"))
	if !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("delete with a stale fence = %v, want ErrFenced", err)
	}

	if got := mustGetBundle(t, h); len(got.Versions) != 2 {
		t.Errorf("versions = %d, want 2", len(got.Versions))
	}
}

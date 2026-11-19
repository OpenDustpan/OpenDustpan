package trustruletest

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/trustrule"
)

// mtlsBundleCases are the cases of RecordExchange for the bundle and the
// bundle version that the service verified. The store checks them again in
// the transaction, so a rotate after the verify refuses the exchange.
func mtlsBundleCases() []suiteCase {
	return []suiteCase{
		{"RecordExchangeStoresAnMtlsTokenWithTheActiveBundleVersion", testBundleActive},
		{"RecordExchangeRefusesAVersionThatIsNoLongerActive", testBundleRotated},
		{"RecordExchangeRefusesAVersionThatIsNotStored", testBundleVersionNotStored},
		{"RecordExchangeRefusesTheActiveVersionOfAnotherNamespace", testBundleVersionOfOtherNamespace},
		{"RecordExchangeRefusesABundleIDThatIsNotTheBundleOfTheRule", testBundleIDOfOtherNamespace},
		{"RecordExchangeRefusesABundleOfNoRow", testBundleNoRow},
		{"RecordExchangeRefusesAnMtlsTokenWithNoBundleVersion", testBundleNoVersion},
		{"RecordExchangeRefusesAJWTTokenWithABundleVersion", testBundleJWTWithVersion},
	}
}

// rotateBundleOf rotates the bundle of ns with a new CA and returns it.
func rotateBundleOf(t *testing.T, h Harness, ns namespace.Name) trustrule.Bundle {
	t.Helper()

	fence := h.Namespace(t, string(ns))

	b, err := h.Bundles.GetBundle(t.Context(), ns)
	if err != nil {
		t.Fatalf("get bundle of %s: %v", ns, err)
	}

	b, err = h.Bundles.RotateBundle(t.Context(), ns, rotateFields(testCA(t), rotateOverlap, b.Revision), fence,
		bundleEntry(ns, "rotate"))
	if err != nil {
		t.Fatalf("rotate bundle of %s: %v", ns, err)
	}

	return b
}

// saveBundleOf saves a bundle of ns with one CA and returns it.
func saveBundleOf(t *testing.T, h Harness, ns namespace.Name) trustrule.Bundle {
	t.Helper()

	fence := h.Namespace(t, string(ns))

	b, err := h.Bundles.SaveBundle(t.Context(), ns, saveFields(testCA(t)), fence, bundleEntry(ns, "save"))
	if err != nil {
		t.Fatalf("save bundle of %s: %v", ns, err)
	}

	return b
}

// refuseBundle records x in acme and checks that the exchange is refused with
// want and leaves no token and no audit entry of its own.
func refuseBundle(t *testing.T, h Harness, x *trustrule.Exchange, ruleID trustrule.ID, want error) {
	t.Helper()

	fence := h.Namespace(t, nsAcme)

	err := h.Exchanges.RecordExchange(t.Context(), *x, fence, auditEntry(nsAcme, "exchange", ruleID))
	mustRefuseMtls(t, h, x, mtlsRefusal{err: err, want: want, entries: oneRule})
}

// testBundleActive stores a token that carries the active version of the
// bundle after a rotate, the version that a verify of the chain used.
func testBundleActive(t *testing.T, h Harness) {
	t.Helper()

	r := mtlsRule(t, h, "deploy", mtlsSPIFFEID)
	rotated := rotateBundleOf(t, h, nsAcme)
	x := mtlsExchangeOf(t, h, &r, mtlsSPIFFEID)

	if x.BundleVersion != rotated.Active {
		t.Fatalf("exchange version = %d, want the active version %d", x.BundleVersion, rotated.Active)
	}

	fence := h.Namespace(t, nsAcme)
	if err := h.Exchanges.RecordExchange(t.Context(), x, fence, auditEntry(nsAcme, "exchange", r.ID)); err != nil {
		t.Fatalf("RecordExchange = %v", err)
	}

	if got, ok := h.Token(t, x.Token.ID); !ok || got.Kind != accesstoken.KindMTLSExchange {
		t.Fatalf("stored token = %+v, %v; want a token of the kind mtls-exchange", got, ok)
	}
}

// testBundleRotated builds the exchange from the active version, then
// rotates the bundle, as a call between the verify of the chain and the record
// would. The version of the exchange is not the active version any more.
func testBundleRotated(t *testing.T, h Harness) {
	t.Helper()

	r := mtlsRule(t, h, "deploy", mtlsSPIFFEID)
	x := mtlsExchangeOf(t, h, &r, mtlsSPIFFEID)

	rotateBundleOf(t, h, nsAcme)
	refuseBundle(t, h, &x, r.ID, trustrule.ErrNotFound)
}

// testBundleVersionNotStored sends a version that the bundle never had.
func testBundleVersionNotStored(t *testing.T, h Harness) {
	t.Helper()

	r := mtlsRule(t, h, "deploy", mtlsSPIFFEID)
	x := mtlsExchangeOf(t, h, &r, mtlsSPIFFEID)
	x.BundleVersion += versionNotStored

	refuseBundle(t, h, &x, r.ID, trustrule.ErrNotFound)
}

// versionNotStored is added to the active version to give a version that the
// bundle never had.
const versionNotStored = 10

// testBundleVersionOfOtherNamespace sends the version number that is active
// in the bundle of a second namespace only: the bundle of acme has one
// version, the bundle of the second namespace has two.
func testBundleVersionOfOtherNamespace(t *testing.T, h Harness) {
	t.Helper()

	r := mtlsRule(t, h, "deploy", mtlsSPIFFEID)
	x := mtlsExchangeOf(t, h, &r, mtlsSPIFFEID)

	saveBundleOf(t, h, nsOther)

	other := rotateBundleOf(t, h, nsOther)
	if other.Active == x.BundleVersion {
		t.Fatalf("version %d is active in both bundles: the case needs two different versions", other.Active)
	}

	x.BundleVersion = other.Active

	refuseBundle(t, h, &x, r.ID, trustrule.ErrNotFound)
}

// testBundleIDOfOtherNamespace sends the ID of the bundle of another
// namespace with the rule of acme, whose bundle is another bundle.
func testBundleIDOfOtherNamespace(t *testing.T, h Harness) {
	t.Helper()

	r := mtlsRule(t, h, "deploy", mtlsSPIFFEID)
	x := mtlsExchangeOf(t, h, &r, mtlsSPIFFEID)
	other := saveBundleOf(t, h, nsOther)
	x.BundleID = other.ID
	x.BundleVersion = other.Active

	refuseBundle(t, h, &x, r.ID, trustrule.ErrNotFound)
}

// testBundleNoRow sends the ID of a bundle that has no row. A bundle that an
// operator removed gives the same state. The stored rule is valid, so only the
// check of the bundle can refuse the exchange.
func testBundleNoRow(t *testing.T, h Harness) {
	t.Helper()

	r := mtlsRule(t, h, "deploy", mtlsSPIFFEID)
	x := mtlsExchangeOf(t, h, &r, mtlsSPIFFEID)
	x.BundleID = trustrule.NewBundleID()

	refuseBundle(t, h, &x, r.ID, trustrule.ErrNotFound)
}

// testBundleNoVersion sends an mTLS exchange with version 0.
func testBundleNoVersion(t *testing.T, h Harness) {
	t.Helper()

	r := mtlsRule(t, h, "deploy", mtlsSPIFFEID)
	x := mtlsExchangeOf(t, h, &r, mtlsSPIFFEID)
	x.BundleVersion = 0

	refuseBundle(t, h, &x, r.ID, trustrule.ErrInvalid)
}

// testBundleJWTWithVersion sends a jwt-exchange token with a bundle version.
func testBundleJWTWithVersion(t *testing.T, h Harness) {
	t.Helper()

	r := mtlsRule(t, h, "deploy", mtlsSPIFFEID)
	x := exchangeOf(&r)
	x.BundleVersion = 1

	refuseBundle(t, h, &x, r.ID, trustrule.ErrInvalid)
}

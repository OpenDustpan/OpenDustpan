package trustruletest

import (
	"errors"
	"strconv"
	"testing"

	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/trustrule"
)

// The numbers of audit entries that remain after a refusal: one create for
// each rule of the fixture.
const (
	oneRule  = 1
	twoRules = 2
)

const (
	mtlsSPIFFEID = "spiffe://example.org/ns/acme/sa/deploy"
	// mtlsWildcard is a pattern that no rule holds, and never a verified
	// SPIFFE ID.
	mtlsWildcard = "spiffe://example.org/ns/acme/sa/*"
)

// mtlsExchangeCases are the cases of RecordExchange for a token of the kind
// mtls-exchange. The store matches the SPIFFE ID again in the transaction.
func mtlsExchangeCases() []suiteCase {
	return []suiteCase{
		{"RecordExchangeStoresAnMtlsToken", testMtlsStores},
		{"RecordExchangeRefusesAnMtlsTokenWithNoSPIFFEID", testMtlsNoSPIFFEID},
		{"RecordExchangeRefusesAJWTTokenWithASPIFFEID", testMtlsJWTWithSPIFFEID},
		{"RecordExchangeRefusesTwoRulesWithOneSPIFFEID", testMtlsTwoRules},
		{"RecordExchangeRefusesARuleAddedAfterTheRead", testMtlsLateRule},
		{"RecordExchangeRefusesARuleOfAnotherBundle", testMtlsOtherBundle},
		{"RecordExchangeRefusesAWildcardSPIFFEID", testMtlsWildcard},
		{"RecordExchangeRefusesTheWrongRuleOfTheSPIFFEID", testMtlsWrongRule},
		{"RecordExchangeRefusesASPIFFEIDOfNoRule", testMtlsNoRule},
		{"RecordExchangeAcceptsTheOneRuleAmongManyRules", testMtlsManyRules},
		{"RecordExchangeRefusesATwinAmongManyRules", testMtlsTwinAmongManyRules},
		{"RecordExchangeIgnoresTheSameSPIFFEIDInAnotherNamespace", testMtlsOtherNamespaceSameSPIFFEID},
	}
}

// mtlsRule saves the bundle of acme, if it has none, and creates a rule of
// acme in that bundle with the SPIFFE ID pattern pattern.
func mtlsRule(t *testing.T, h Harness, name, pattern string) trustrule.Rule {
	t.Helper()

	fence := h.Namespace(t, nsAcme)

	b, err := h.Bundles.GetBundle(t.Context(), nsAcme)
	if errors.Is(err, trustrule.ErrNotFound) {
		b, err = h.Bundles.SaveBundle(t.Context(), nsAcme, saveFields(testCA(t)), fence,
			bundleEntry(nsAcme, "save"))
	}

	if err != nil {
		t.Fatalf("bundle of %s: %v", nsAcme, err)
	}

	r := newRule(nsAcme, name, workload(t, h, nsAcme))
	r.Bundle = string(b.ID)
	r.SpiffeIDPattern = pattern
	mustCreate(t, h, r)

	return r
}

// mtlsExchangeOf returns an exchange of a new token of the kind
// mtls-exchange through r with the SPIFFE ID spiffeID.
// The exchange carries the bundle and the active version that the bundle of
// acme has now, as a verify of the chain would.
func mtlsExchangeOf(t *testing.T, h Harness, r *trustrule.Rule, spiffeID string) trustrule.Exchange {
	t.Helper()

	b, err := h.Bundles.GetBundle(t.Context(), nsAcme)
	if err != nil {
		t.Fatalf("get bundle: %v", err)
	}

	x := exchangeOf(r)
	x.Token.Kind = accesstoken.KindMTLSExchange
	x.SPIFFEID = spiffeID
	x.BundleID = b.ID
	x.BundleVersion = b.Active

	return x
}

// mtlsRefusal is the error that a refused exchange returns, and the number of
// audit entries that must remain: the creates of the rules.
type mtlsRefusal struct {
	err, want error
	entries   int
}

// mustRefuseMtls fails the test unless err is want, no token x.Token exists,
// and only the creates of the rules wrote an entry.
func mustRefuseMtls(t *testing.T, h Harness, x *trustrule.Exchange, r mtlsRefusal) {
	t.Helper()

	if !errors.Is(r.err, r.want) {
		t.Fatalf("RecordExchange = %v, want %v", r.err, r.want)
	}

	if _, ok := h.Token(t, x.Token.ID); ok {
		t.Fatalf("the refused exchange stored the token %s", x.Token.ID)
	}

	if got := auditCount(t, h, nsAcme); got != r.entries {
		t.Fatalf("audit entries = %d, want %d: no entry of the refused exchange", got, r.entries)
	}
}

func testMtlsStores(t *testing.T, h Harness) {
	t.Helper()

	r := mtlsRule(t, h, "deploy", mtlsSPIFFEID)
	_ = mtlsRule(t, h, "other", "spiffe://example.org/ns/acme/sa/other")
	x := mtlsExchangeOf(t, h, &r, mtlsSPIFFEID)
	fence := h.Namespace(t, nsAcme)

	if err := h.Exchanges.RecordExchange(t.Context(), x, fence, auditEntry(nsAcme, "exchange", r.ID)); err != nil {
		t.Fatalf("RecordExchange = %v", err)
	}

	got, ok := h.Token(t, x.Token.ID)
	if !ok || got.Kind != accesstoken.KindMTLSExchange || got.PrincipalID != r.PrincipalID ||
		!got.ExpiresAt.Equal(x.Token.ExpiresAt) {
		t.Fatalf("stored token = %+v, %v; want %+v", got, ok, x.Token)
	}

	entries := ruleEntries(t, h, nsAcme)
	if len(entries) != 3 || entries[2].Action != "trust-rule:exchange" {
		t.Fatalf("audit entries = %+v, want the two creates and the exchange", entries)
	}
}

func testMtlsNoSPIFFEID(t *testing.T, h Harness) {
	t.Helper()

	r := mtlsRule(t, h, "deploy", mtlsSPIFFEID)
	x := mtlsExchangeOf(t, h, &r, "")
	fence := h.Namespace(t, nsAcme)

	err := h.Exchanges.RecordExchange(t.Context(), x, fence, auditEntry(nsAcme, "exchange", r.ID))
	mustRefuseMtls(t, h, &x, mtlsRefusal{err: err, want: trustrule.ErrInvalid, entries: oneRule})
}

func testMtlsJWTWithSPIFFEID(t *testing.T, h Harness) {
	t.Helper()

	r := mtlsRule(t, h, "deploy", mtlsSPIFFEID)
	x := exchangeOf(&r)
	x.SPIFFEID = mtlsSPIFFEID
	fence := h.Namespace(t, nsAcme)

	err := h.Exchanges.RecordExchange(t.Context(), x, fence, auditEntry(nsAcme, "exchange", r.ID))
	mustRefuseMtls(t, h, &x, mtlsRefusal{err: err, want: trustrule.ErrInvalid, entries: oneRule})
}

func testMtlsTwoRules(t *testing.T, h Harness) {
	t.Helper()

	r := mtlsRule(t, h, "deploy", mtlsSPIFFEID)
	_ = mtlsRule(t, h, "twin", mtlsSPIFFEID)
	x := mtlsExchangeOf(t, h, &r, mtlsSPIFFEID)
	fence := h.Namespace(t, nsAcme)

	err := h.Exchanges.RecordExchange(t.Context(), x, fence, auditEntry(nsAcme, "exchange", r.ID))
	mustRefuseMtls(t, h, &x, mtlsRefusal{err: err, want: trustrule.ErrNotFound, entries: twoRules})
}

// testMtlsLateRule sends an exchange that the service matched while one rule
// named the SPIFFE ID, after another call added a second rule for it.
func testMtlsLateRule(t *testing.T, h Harness) {
	t.Helper()

	r := mtlsRule(t, h, "deploy", mtlsSPIFFEID)
	fence := h.Namespace(t, nsAcme)

	first := mtlsExchangeOf(t, h, &r, mtlsSPIFFEID)
	if err := h.Exchanges.RecordExchange(t.Context(), first, fence, auditEntry(nsAcme, "exchange", r.ID)); err != nil {
		t.Fatalf("RecordExchange before the second rule = %v", err)
	}

	_ = mtlsRule(t, h, "late", mtlsSPIFFEID)
	second := mtlsExchangeOf(t, h, &r, mtlsSPIFFEID)

	err := h.Exchanges.RecordExchange(t.Context(), second, fence, auditEntry(nsAcme, "exchange", r.ID))
	if !errors.Is(err, trustrule.ErrNotFound) {
		t.Fatalf("RecordExchange after the second rule = %v, want ErrNotFound", err)
	}

	if _, ok := h.Token(t, second.Token.ID); ok {
		t.Fatalf("the refused exchange stored the token %s", second.Token.ID)
	}
}

// testMtlsOtherBundle sends the SPIFFE ID of a rule in the bundle to a rule
// that is not in that bundle, and the SPIFFE ID of the rule of the exchange
// that no rule of its bundle names.
func testMtlsOtherBundle(t *testing.T, h Harness) {
	t.Helper()

	named := mtlsRule(t, h, "deploy", mtlsSPIFFEID)
	fence := h.Namespace(t, nsAcme)

	// A rule with no bundle and no SPIFFE ID pattern: the bundle of the rule
	// of the exchange is not the bundle that names the SPIFFE ID.
	plain := newRule(nsAcme, "plain", workload(t, h, nsAcme))
	mustCreate(t, h, plain)

	x := mtlsExchangeOf(t, h, &plain, mtlsSPIFFEID)

	err := h.Exchanges.RecordExchange(t.Context(), x, fence, auditEntry(nsAcme, "exchange", plain.ID))
	mustRefuseMtls(t, h, &x, mtlsRefusal{err: err, want: trustrule.ErrNotFound, entries: twoRules})

	// The rule of the exchange is in the bundle, and no rule of the bundle
	// names the SPIFFE ID that the exchange carries.
	y := mtlsExchangeOf(t, h, &named, "spiffe://example.org/ns/acme/sa/unnamed")

	err = h.Exchanges.RecordExchange(t.Context(), y, fence, auditEntry(nsAcme, "exchange", named.ID))
	mustRefuseMtls(t, h, &y, mtlsRefusal{err: err, want: trustrule.ErrNotFound, entries: twoRules})
}

// testMtlsWildcard sends a pattern with a * as the SPIFFE ID of an exchange,
// although a rule of the bundle names an exact SPIFFE ID.
func testMtlsWildcard(t *testing.T, h Harness) {
	t.Helper()

	r := mtlsRule(t, h, "wildcard", mtlsSPIFFEID)
	x := mtlsExchangeOf(t, h, &r, mtlsWildcard)
	fence := h.Namespace(t, nsAcme)

	err := h.Exchanges.RecordExchange(t.Context(), x, fence, auditEntry(nsAcme, "exchange", r.ID))
	mustRefuseMtls(t, h, &x, mtlsRefusal{err: err, want: trustrule.ErrInvalid, entries: oneRule})
}

// testMtlsWrongRule sends an exchange through rule R1, which names another
// pattern, with the SPIFFE ID that only rule R2 names. R2 belongs to another
// principal. Exactly one rule names the SPIFFE ID, and it is not the rule of
// the exchange, so the token must not go to the principal of R1.
func testMtlsWrongRule(t *testing.T, h Harness) {
	t.Helper()

	wrong := mtlsRule(t, h, "other", "spiffe://example.org/ns/acme/sa/other")
	_ = mtlsRule(t, h, "deploy", mtlsSPIFFEID)
	x := mtlsExchangeOf(t, h, &wrong, mtlsSPIFFEID)
	fence := h.Namespace(t, nsAcme)

	err := h.Exchanges.RecordExchange(t.Context(), x, fence, auditEntry(nsAcme, "exchange", wrong.ID))
	mustRefuseMtls(t, h, &x, mtlsRefusal{err: err, want: trustrule.ErrNotFound, entries: twoRules})
}

// testMtlsNoRule sends a SPIFFE ID that no rule of the bundle names.
func testMtlsNoRule(t *testing.T, h Harness) {
	t.Helper()

	r := mtlsRule(t, h, "deploy", mtlsSPIFFEID)
	_ = mtlsRule(t, h, "other", "spiffe://example.org/ns/acme/sa/other")
	x := mtlsExchangeOf(t, h, &r, "spiffe://example.org/ns/acme/sa/nobody")
	fence := h.Namespace(t, nsAcme)

	err := h.Exchanges.RecordExchange(t.Context(), x, fence, auditEntry(nsAcme, "exchange", r.ID))
	mustRefuseMtls(t, h, &x, mtlsRefusal{err: err, want: trustrule.ErrNotFound, entries: twoRules})
}

// testMtlsManyRules fills one namespace to its cap of MaxPerNamespace rules
// with other SPIFFE IDs and the one rule of the exchange. The exchange is
// recorded, and the read of the recheck does not grow with the size of the
// namespace. The port holds no more rules than the cap, so the proof with
// more rules stays in the adapters.
func testMtlsManyRules(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, nsAcme)
	for i := range trustrule.MaxPerNamespace - 1 {
		mtlsRule(t, h, "other-"+fmtInt(i), "spiffe://example.org/ns/acme/sa/other-"+fmtInt(i))
	}

	r := mtlsRule(t, h, "deploy", mtlsSPIFFEID)
	x := mtlsExchangeOf(t, h, &r, mtlsSPIFFEID)

	if err := h.Exchanges.RecordExchange(t.Context(), x, fence, auditEntry(nsAcme, "exchange", r.ID)); err != nil {
		t.Fatalf("RecordExchange = %v", err)
	}

	if _, ok := h.Token(t, x.Token.ID); !ok {
		t.Fatal("token not stored")
	}
}

// testMtlsTwinAmongManyRules fills one namespace to its cap of
// MaxPerNamespace rules: other SPIFFE IDs, the rule of the exchange and a
// twin of the same bundle. The exchange is refused.
func testMtlsTwinAmongManyRules(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, nsAcme)
	for i := range trustrule.MaxPerNamespace - 2 {
		mtlsRule(t, h, "other-"+fmtInt(i), "spiffe://example.org/ns/acme/sa/other-"+fmtInt(i))
	}

	r := mtlsRule(t, h, "deploy", mtlsSPIFFEID)
	_ = mtlsRule(t, h, "twin", mtlsSPIFFEID)
	x := mtlsExchangeOf(t, h, &r, mtlsSPIFFEID)

	err := h.Exchanges.RecordExchange(t.Context(), x, fence, auditEntry(nsAcme, "exchange", r.ID))
	mustRefuseMtls(t, h, &x, mtlsRefusal{err: err, want: trustrule.ErrNotFound, entries: trustrule.MaxPerNamespace})
}

// testMtlsOtherNamespaceSameSPIFFEID stores a rule of another namespace with
// the same SPIFFE ID. The exchange in acme is recorded.
func testMtlsOtherNamespaceSameSPIFFEID(t *testing.T, h Harness) {
	t.Helper()

	acme := h.Namespace(t, nsAcme)

	_ = mtlsRuleIn(t, h, nsOther, "other", mtlsSPIFFEID)
	r := mtlsRule(t, h, "deploy", mtlsSPIFFEID)
	x := mtlsExchangeOf(t, h, &r, mtlsSPIFFEID)

	if err := h.Exchanges.RecordExchange(t.Context(), x, acme, auditEntry(nsAcme, "exchange", r.ID)); err != nil {
		t.Fatalf("RecordExchange = %v", err)
	}

	if _, ok := h.Token(t, x.Token.ID); !ok {
		t.Fatal("token not stored")
	}
}

// mtlsRuleIn makes the namespace ns when it does not exist, saves its bundle
// and creates a rule of ns in that bundle with the SPIFFE ID pattern pattern.
func mtlsRuleIn(t *testing.T, h Harness, ns, name, pattern string) trustrule.Rule {
	t.Helper()

	fence := h.Namespace(t, ns)
	id := h.Principal(t, namespace.Name(ns), principal.KindService)

	b, err := h.Bundles.GetBundle(t.Context(), namespace.Name(ns))
	if errors.Is(err, trustrule.ErrNotFound) {
		b, err = h.Bundles.SaveBundle(t.Context(), namespace.Name(ns), saveFields(testCA(t)), fence,
			bundleEntry(namespace.Name(ns), "save"))
	}
	if err != nil {
		t.Fatalf("bundle of %s: %v", ns, err)
	}

	r := newRule(namespace.Name(ns), name, id)
	r.Bundle = string(b.ID)
	r.SpiffeIDPattern = pattern
	mustCreate(t, h, r)

	return r
}

// fmtInt formats a small integer for test names and IDs.
func fmtInt(i int) string {
	return strconv.Itoa(i)
}

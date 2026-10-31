package trustruletest

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/trustrule"
)

// exchangeCases are the cases of RecordExchange.
func exchangeCases() []suiteCase {
	return []suiteCase{
		{"RecordExchangeStoresTheTokenAndItsEntry", testExchangeStores},
		{"RecordExchangeRefusesAnUnknownRule", testExchangeUnknownRule},
		{"RecordExchangeRefusesARuleOfAnotherNamespace", testExchangeOtherNamespace},
		{"RecordExchangeRefusesAnotherPrincipal", testExchangeOtherPrincipal},
		{"RecordExchangeRefusesADeletedOrDisabledPrincipal", testExchangeEndedPrincipal},
		{"RecordExchangeRefusesATokenOfAnotherKind", testExchangeOtherKind},
		{"RecordExchangeRefusesAStaleFence", testExchangeStaleFence},
		{"RecordExchangeRefusesATakenDigest", testExchangeTakenDigest},
	}
}

// trustToken returns a new token of the kind trust for principalID, and the
// digest of a new secret.
func trustToken(principalID string) (accesstoken.Token, accesstoken.Digest) {
	at := time.UnixMilli(testTimeMs).UTC()

	t := accesstoken.Token{
		CreatedAt: at, ExpiresAt: at.Add(time.Hour), LastUsedAt: time.Time{}, RevokedAt: time.Time{},
		DisabledAt: time.Time{}, ID: ident.NewID("tok-"), PrincipalID: principalID, Name: "trust rule",
		Kind: accesstoken.KindTrust, ReplacedBy: "", CreatedBy: "trust-rule",
	}

	var d accesstoken.Digest

	copy(d[:], ident.NewID("d-"))

	return t, d
}

// exchangeOf returns an exchange of a new trust token through r.
func exchangeOf(r *trustrule.Rule) trustrule.Exchange {
	tok, d := trustToken(r.PrincipalID)

	return trustrule.Exchange{Token: tok, Digest: d, Rule: r.ID}
}

// exchangeFixture makes a rule in acme and returns it with the current fence.
func exchangeFixture(t *testing.T, h Harness) (trustrule.Rule, namespace.Fence) {
	t.Helper()

	fence := h.Namespace(t, nsAcme)
	r := newRule(nsAcme, "deploy", workload(t, h, nsAcme))
	mustCreate(t, h, r)

	return r, fence
}

// mustRefuse fails the test unless err is want, no token x.Token exists,
// and only the create of the rule wrote an entry.
func mustRefuse(t *testing.T, h Harness, x *trustrule.Exchange, err, want error) {
	t.Helper()

	if !errors.Is(err, want) {
		t.Fatalf("RecordExchange = %v, want %v", err, want)
	}

	if _, ok := h.Token(t, x.Token.ID); ok {
		t.Fatalf("the refused exchange stored the token %s", x.Token.ID)
	}

	if got := auditCount(t, h, nsAcme); got != 1 {
		t.Fatalf("audit entries = %d, want only the create of the rule", got)
	}
}

func testExchangeStores(t *testing.T, h Harness) {
	t.Helper()

	r, fence := exchangeFixture(t, h)
	x := exchangeOf(&r)

	if err := h.Exchanges.RecordExchange(t.Context(), x, fence, auditEntry(nsAcme, "exchange", r.ID)); err != nil {
		t.Fatalf("RecordExchange = %v", err)
	}

	got, ok := h.Token(t, x.Token.ID)
	if !ok || got.Kind != accesstoken.KindTrust || got.PrincipalID != r.PrincipalID ||
		!got.ExpiresAt.Equal(x.Token.ExpiresAt) {
		t.Fatalf("stored token = %+v, %v; want %+v", got, ok, x.Token)
	}

	entries := ruleEntries(t, h, nsAcme)
	if len(entries) != 2 || entries[1].Action != "trust-rule:exchange" {
		t.Fatalf("audit entries = %+v, want the create and the exchange", entries)
	}
}

func testExchangeUnknownRule(t *testing.T, h Harness) {
	t.Helper()

	r, fence := exchangeFixture(t, h)
	other := r
	other.ID = trustrule.NewID()
	x := exchangeOf(&other)

	err := h.Exchanges.RecordExchange(t.Context(), x, fence, auditEntry(nsAcme, "exchange", other.ID))
	mustRefuse(t, h, &x, err, trustrule.ErrNotFound)
}

func testExchangeOtherNamespace(t *testing.T, h Harness) {
	t.Helper()

	r, _ := exchangeFixture(t, h)
	other := h.Namespace(t, nsOther)
	x := exchangeOf(&r)

	err := h.Exchanges.RecordExchange(t.Context(), x, other, auditEntry(nsOther, "exchange", r.ID))
	mustRefuse(t, h, &x, err, trustrule.ErrNotFound)
}

func testExchangeOtherPrincipal(t *testing.T, h Harness) {
	t.Helper()

	r, fence := exchangeFixture(t, h)
	x := exchangeOf(&r)
	x.Token.PrincipalID = workload(t, h, nsAcme)

	err := h.Exchanges.RecordExchange(t.Context(), x, fence, auditEntry(nsAcme, "exchange", r.ID))
	mustRefuse(t, h, &x, err, trustrule.ErrNotFound)
}

func testExchangeEndedPrincipal(t *testing.T, h Harness) {
	t.Helper()

	for name, end := range map[string]func(t *testing.T, id string){
		"deleted": h.DeletePrincipal, "disabled": h.DisablePrincipal,
	} {
		fence := h.Namespace(t, nsAcme)
		r := newRule(nsAcme, "deploy "+name, workload(t, h, nsAcme))
		mustCreate(t, h, r)
		end(t, r.PrincipalID)

		x := exchangeOf(&r)

		err := h.Exchanges.RecordExchange(t.Context(), x, fence, auditEntry(nsAcme, "exchange", r.ID))
		if !errors.Is(err, trustrule.ErrPrincipal) {
			t.Errorf("a %s principal: RecordExchange = %v, want ErrPrincipal", name, err)
		}

		if _, ok := h.Token(t, x.Token.ID); ok {
			t.Errorf("a %s principal: the refused exchange stored its token", name)
		}
	}
}

func testExchangeOtherKind(t *testing.T, h Harness) {
	t.Helper()

	r, fence := exchangeFixture(t, h)

	for _, kind := range []accesstoken.Kind{accesstoken.KindService, accesstoken.KindPersonal, ""} {
		x := exchangeOf(&r)
		x.Token.Kind = kind

		err := h.Exchanges.RecordExchange(t.Context(), x, fence, auditEntry(nsAcme, "exchange", r.ID))
		mustRefuse(t, h, &x, err, trustrule.ErrInvalid)
	}
}

func testExchangeStaleFence(t *testing.T, h Harness) {
	t.Helper()

	r, fence := exchangeFixture(t, h)
	x := exchangeOf(&r)

	err := h.Exchanges.RecordExchange(t.Context(), x, staleFence(fence), auditEntry(nsAcme, "exchange", r.ID))
	mustRefuse(t, h, &x, err, namespace.ErrFenced)
}

func testExchangeTakenDigest(t *testing.T, h Harness) {
	t.Helper()

	r, fence := exchangeFixture(t, h)
	first := exchangeOf(&r)

	if err := h.Exchanges.RecordExchange(t.Context(), first, fence, auditEntry(nsAcme, "exchange", r.ID)); err != nil {
		t.Fatalf("RecordExchange = %v", err)
	}

	second := exchangeOf(&r)
	second.Digest = first.Digest

	err := h.Exchanges.RecordExchange(t.Context(), second, fence, auditEntry(nsAcme, "exchange", r.ID))
	if !errors.Is(err, accesstoken.ErrAlreadyExists) {
		t.Fatalf("RecordExchange with a taken digest = %v, want accesstoken.ErrAlreadyExists", err)
	}

	if _, ok := h.Token(t, second.Token.ID); ok {
		t.Fatal("the refused exchange stored its token")
	}
}

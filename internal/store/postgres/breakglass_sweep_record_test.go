package postgres_test

import (
	"os"
	"slices"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/store/postgres"
	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// postgresSweepFixture holds the services over one PostgreSQL store. Its clock
// starts the inactivity period and one hour before a fixed date in the past, so
// a record that it creates is in the past of the database clock of the sweep.
type postgresSweepFixture struct {
	principals *principal.Service
	store      *postgres.Store
	svc        *accesstoken.Service
	now        func() time.Time
}

func newPostgresSweepFixture(t *testing.T) postgresSweepFixture {
	t.Helper()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL break-glass sweep record test", envURL)
	}

	store := open(t, base)
	at := time.Date(2026, time.November, 5, 9, 0, 0, 0, time.UTC).Add(-accesstoken.DefaultInactivity - time.Hour)
	now := func() time.Time { return at }

	principals, err := principal.NewService(principal.Config{
		TempAdmins: nil,
		Store:      store,
		Guard:      principal.AdminOnlyGuard{},
		Namespaces: nil,
		Tokens:     nil,
		GroupReach: nil,
		Now:        now,
		NewID:      nil,
	})
	if err != nil {
		t.Fatalf("principal service: %v", err)
	}

	return postgresSweepFixture{
		principals: principals, store: store, svc: tokenService(t, store, now), now: now,
	}
}

// seed sets the account part of the sweep on and stores one inactive
// break-glass account.
func (f postgresSweepFixture) seed(t *testing.T) principal.Principal {
	t.Helper()

	ctx := t.Context()
	_, err := f.svc.SetPolicy(ctx, policyCaller(), accesstoken.PolicyChange{
		MaxLifetime: nil, Inactivity: nil,
		DisableTokens: new(false), DisableAccounts: new(true), DisableBreakGlass: nil, ExchangeLifetime: nil,
	})
	if err != nil {
		t.Fatalf("set policy: %v", err)
	}

	at := f.now()
	bg := principal.Principal{
		CreatedAt: at, UpdatedAt: at, DisabledAt: time.Time{}, ID: "prn-bg-sweep",
		Subject: "bg@example.com", DisplayName: "bg@example.com", Kind: principal.KindPerson,
		Source: principal.SourceLocal, Namespace: "", GroupCount: 0, BreakGlass: true,
	}

	if err = f.store.CreatePrincipal(ctx, bg, storetest.ServerChange(at, principal.Ref(bg.ID))); err != nil {
		t.Fatalf("create break-glass account: %v", err)
	}

	return bg
}

func (f postgresSweepFixture) sweep(t *testing.T) accesstoken.SweepResult {
	t.Helper()

	res, err := f.svc.SweepInactive(t.Context(), accesstoken.SweepEntry())
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}

	return res
}

// TestBreakGlassSweepWritesExemptRecord proves that the PostgreSQL adapter
// keeps an inactive break-glass account enabled and writes one audit record
// for it, with the shape of the sweep record and the exemption reason.
func TestBreakGlassSweepWritesExemptRecord(t *testing.T) {
	t.Parallel()

	f := newPostgresSweepFixture(t)
	bg := f.seed(t)
	checkPostgresExemptRecord(t, f, f.sweep(t), bg)
}

// TestBreakGlassSweepFailureStoresNothing proves on a real PostgreSQL store
// that a sweep fails closed through SweepInactive: when the clock read, the
// disable of the tokens, the disable of the accounts or the list of the
// skipped break-glass accounts fails, the transaction of the store rolls
// back. No account is disabled, and the server audit scope holds no new
// record. The list fails after the disable of the accounts ran, so that case
// shows that the rollback undoes a disable. After the fault is removed, the
// same sweep disables the inactive person, so the seed is one that the failed
// sweep would have changed.
func TestBreakGlassSweepFailureStoresNothing(t *testing.T) {
	t.Parallel()

	for _, name := range []string{
		"DatabaseNowMs",
		"DisableInactiveAccessTokens",
		"DisableInactivePrincipals",
		"ListSweepSkippedBreakGlassPrincipals",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			checkSweepFailureStoresNothing(t, name)
		})
	}
}

func checkSweepFailureStoresNothing(t *testing.T, name string) {
	t.Helper()

	f := newPostgresSweepFixture(t)
	bg := f.seed(t)
	idle := f.seedIdlePerson(t)
	before := f.serverEntryCount(t)

	fault := postgres.FailStatementForTest(f.store, name)

	res, err := f.svc.SweepInactive(t.Context(), accesstoken.SweepEntry())
	if err == nil {
		t.Fatalf("sweep with %s failing = %+v, want an error", name, res)
	}

	if fault.Fired() == 0 {
		t.Fatalf("the fault of %s did not fire", name)
	}

	fault.Restore()

	for _, id := range []string{idle.ID, bg.ID} {
		stored, getErr := f.principals.GetPrincipal(t.Context(), id)
		if getErr != nil || !stored.DisabledAt.IsZero() {
			t.Fatalf("principal %s after the failed sweep = %+v, %v; want it enabled", id, stored, getErr)
		}
	}

	if after := f.serverEntryCount(t); after != before {
		t.Fatalf("server audit entries after the failed sweep = %d, want %d", after, before)
	}

	if clean := f.sweep(t); !slices.Contains(clean.Principals, idle.ID) {
		t.Fatalf("sweep without the fault = %+v, want the inactive person %q disabled", clean, idle.ID)
	}
}

// seedIdlePerson turns the token part of the sweep on and stores one
// inactive person that is not a break-glass account.
func (f postgresSweepFixture) seedIdlePerson(t *testing.T) principal.Principal {
	t.Helper()

	ctx := t.Context()
	_, err := f.svc.SetPolicy(ctx, policyCaller(), accesstoken.PolicyChange{
		MaxLifetime: nil, Inactivity: nil,
		DisableTokens: new(true), DisableAccounts: new(true), DisableBreakGlass: nil, ExchangeLifetime: nil,
	})
	if err != nil {
		t.Fatalf("set policy: %v", err)
	}

	p, err := f.principals.CreatePrincipal(ctx, policyCaller(), principal.Input{
		Subject: "idle@example.com", DisplayName: "", Kind: principal.KindPerson,
	})
	if err != nil {
		t.Fatalf("create person: %v", err)
	}

	return p
}

// serverEntryCount returns the count of the entries in the server audit
// scope.
func (f postgresSweepFixture) serverEntryCount(t *testing.T) int {
	t.Helper()

	var n int

	err := postgres.Pool(f.store).QueryRow(t.Context(),
		"SELECT count(*) FROM audit_entries WHERE namespace = $1", audit.ServerScope.String()).Scan(&n)
	if err != nil {
		t.Fatalf("count the server audit entries: %v", err)
	}

	return n
}

func checkPostgresExemptRecord(
	t *testing.T, f postgresSweepFixture, res accesstoken.SweepResult, bg principal.Principal,
) {
	t.Helper()

	ctx := t.Context()

	if slices.Contains(res.Principals, bg.ID) {
		t.Fatalf("sweep = %+v, want the break-glass account %q not disabled", res, bg.ID)
	}

	stored, err := f.principals.GetPrincipal(ctx, bg.ID)
	if err != nil || !stored.BreakGlass || !stored.DisabledAt.IsZero() {
		t.Fatalf("break-glass account = %+v, %v; want it enabled with the flag", stored, err)
	}

	listing, err := f.store.List(ctx, audit.ServerScope, audit.Page{Size: audit.DefaultPageSize, Before: 0})
	if err != nil {
		t.Fatalf("list @server entries: %v", err)
	}

	checkPostgresExemptEntries(t, listing.Entries, principal.Ref(bg.ID))
}

// checkPostgresExemptEntries proves that listing holds exactly one exemption
// record for object, with the shape of the sweep record.
func checkPostgresExemptEntries(t *testing.T, entries []audit.Entry, object string) {
	t.Helper()

	count := 0

	for i := range entries {
		e := &entries[i]
		if e.Object != object || e.Reason != "break-glass account is exempt" {
			continue
		}

		count++
		checkPostgresExemptEntryShape(t, e)
	}

	if count != 1 {
		t.Fatalf("exemption records for %s = %d, want 1", object, count)
	}
}

func checkPostgresExemptEntryShape(t *testing.T, e *audit.Entry) {
	t.Helper()

	if e.Kind != audit.KindChange || e.Outcome != audit.OutcomeSuccess ||
		e.Action != string(identity.ActionServerManageTokenPolicy) {
		t.Fatalf("exemption record = %+v, want one change of outcome success with the sweep action", e)
	}
}

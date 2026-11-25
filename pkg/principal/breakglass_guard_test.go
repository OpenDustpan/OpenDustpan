package principal_test

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/store/sqlite"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/principal"
)

// breakGlassChange is one covered write of a break-glass account.
type breakGlassChange func(t *testing.T, svc *principal.Service, id string) error

// breakGlassGuardCase is one row of the guard test: the accounts that exist,
// the write and the refusal that the row wants.
type breakGlassGuardCase struct {
	want        error
	name        string
	target      string
	change      breakGlassChange
	accounts    []string
	wantEnabled int
	wantEntries int
}

func TestBreakGlassGuard(t *testing.T) {
	t.Parallel()

	for _, tc := range []breakGlassGuardCase{
		{
			name: "disable the last enabled account is refused", change: disableBreakGlass,
			accounts: []string{"prn-bg1"}, target: "prn-bg1", want: principal.ErrLastBreakGlass,
			wantEnabled: 1, wantEntries: 0,
		},
		{
			name: "delete the last enabled account is refused", change: deleteBreakGlass,
			accounts: []string{"prn-bg1"}, target: "prn-bg1", want: principal.ErrLastBreakGlass,
			wantEnabled: 1, wantEntries: 0,
		},
		{
			name: "disable one of two enabled accounts passes", change: disableBreakGlass,
			accounts: []string{"prn-bg1", "prn-bg2"}, target: "prn-bg1", want: nil,
			wantEnabled: 1, wantEntries: 1,
		},
		{
			name: "delete one of two enabled accounts passes", change: deleteBreakGlass,
			accounts: []string{"prn-bg1", "prn-bg2"}, target: "prn-bg1", want: nil,
			wantEnabled: 1, wantEntries: 1,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			runBreakGlassGuardCase(t, tc)
		})
	}
}

// runBreakGlassGuardCase stores the accounts of the row, sends its change and
// checks the answer, the live count and the entry that the path writes.
func runBreakGlassGuardCase(t *testing.T, tc breakGlassGuardCase) {
	t.Helper()

	svc := newBreakGlassService(t)

	for i, id := range tc.accounts {
		storeBreakGlass(t, svc.store, id, fmt.Sprintf("person-%d", i+1))
	}

	got := tc.change(t, svc.service, tc.target)
	checkGuardAnswer(t, got, tc.want)

	checkGuardLiveCount(t, svc, tc.wantEnabled)
	checkGuardAuditEntry(t, svc, tc)
}

// checkGuardAnswer proves the answer of the row: nil for a change that passes,
// and the sentinel of the refusal, which also matches the older sentinel, for
// one that does not.
func checkGuardAnswer(t *testing.T, got, want error) {
	t.Helper()

	if want == nil {
		if got != nil {
			t.Fatalf("got %v, want nil", got)
		}

		return
	}

	if !errors.Is(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}

	// The older sentinel keeps its meaning: a change that leaves no enabled
	// account also leaves too few working accounts.
	if !errors.Is(got, principal.ErrNoWorkingBreakGlass) {
		t.Fatalf("got %v, want an error that matches %v", got, principal.ErrNoWorkingBreakGlass)
	}
}

// checkGuardLiveCount proves that the number of enabled break-glass accounts
// after the change is the one of the row. A refused change leaves the count
// as it was; a disable or a delete lowers it by one.
func checkGuardLiveCount(t *testing.T, svc breakGlassService, want int) {
	t.Helper()

	accounts, err := svc.service.ListBreakGlassAccounts(t.Context())
	if err != nil {
		t.Fatalf("list break-glass accounts: %v", err)
	}

	enabled := 0
	for i := range accounts {
		if accounts[i].DisabledAt.IsZero() {
			enabled++
		}
	}

	if enabled != want {
		t.Fatalf("enabled break-glass accounts = %d, want %d", enabled, want)
	}
}

// checkGuardAuditEntry proves the entry that the path writes for its target:
// one change of outcome success for a disable or a delete that passes, and
// none for one that the store refuses. The denial of a refusal is written by
// the HTTP handler, not by this service path.
func checkGuardAuditEntry(t *testing.T, svc breakGlassService, tc breakGlassGuardCase) {
	t.Helper()

	listing, err := svc.store.List(t.Context(), audit.ServerScope,
		audit.Page{Size: audit.DefaultPageSize, Before: 0})
	if err != nil {
		t.Fatalf("list @server entries: %v", err)
	}

	object := principal.Ref(tc.target)
	var entries []audit.Entry

	for i := range listing.Entries {
		e := &listing.Entries[i]
		if e.Object == object && e.Action == string(identity.ActionServerManagePrincipals) {
			entries = append(entries, *e)
		}
	}

	if len(entries) != tc.wantEntries {
		t.Fatalf("entries on %s = %d, want %d", object, len(entries), tc.wantEntries)
	}

	if tc.wantEntries > 0 && (entries[0].Kind != audit.KindChange || entries[0].Outcome != audit.OutcomeSuccess) {
		t.Fatalf("entry on %s = %+v, want a change of outcome success", object, entries[0])
	}
}

// TestBreakGlassGuardRace proves that two disables of the last two enabled
// accounts cannot both pass: the store counts in the transaction of the
// write, so exactly one request is refused and one account stays enabled. The
// sign-in during a disable is TestPasskeySignInBreakGlassDuringDisable in
// internal/api/server: the sign-in path is in that package, not here.
func TestBreakGlassGuardRace(t *testing.T) {
	t.Parallel()

	svc := newBreakGlassService(t)
	storeBreakGlass(t, svc.store, "prn-bg1", "person-1")
	storeBreakGlass(t, svc.store, "prn-bg2", "person-2")

	ids := []string{"prn-bg1", "prn-bg2"}
	results := make([]error, len(ids))

	var wg sync.WaitGroup

	for i, id := range ids {
		wg.Go(func() {
			results[i] = disableBreakGlass(t, svc.service, id)
		})
	}

	wg.Wait()

	refused := 0

	for i, err := range results {
		switch {
		case err == nil:
		case errors.Is(err, principal.ErrLastBreakGlass):
			refused++
		default:
			t.Fatalf("disable %q = %v, want nil or %v", ids[i], err, principal.ErrLastBreakGlass)
		}
	}

	if refused != 1 {
		t.Fatalf("%d of 2 disables were refused, want 1", refused)
	}

	enabled := 0

	for _, id := range ids {
		got, err := svc.service.GetPrincipal(t.Context(), id)
		if err != nil {
			t.Fatalf("read principal %q: %v", id, err)
		}

		if got.DisabledAt.IsZero() {
			enabled++
		}
	}

	if enabled != 1 {
		t.Fatalf("%d of 2 break-glass accounts stay enabled, want 1", enabled)
	}
}

// breakGlassService is a service over a fresh store, with a guard that lets an
// administrator change the break-glass accounts.
type breakGlassService struct {
	store   *sqlite.Store
	service *principal.Service
}

func newBreakGlassService(t *testing.T) breakGlassService {
	t.Helper()

	st := openStore(t)

	svc, err := principal.NewService(principal.Config{
		TempAdmins: nil,
		Store:      st,
		Guard:      principal.AdminOnlyGuard{},
		Namespaces: nil,
		Tokens:     nil,
		GroupReach: nil,
		Now:        nil,
		NewID:      nil,
	})
	if err != nil {
		t.Fatalf("service: %v", err)
	}

	return breakGlassService{store: st, service: svc}
}

// siteAdministrator is the caller of the guard test. It holds the
// administrator flag that AdminOnlyGuard needs.
func siteAdministrator() identity.Principal {
	return identity.Principal{
		Subject: "admin", DisplayName: "admin", Admin: true, TokenID: "",
		TokenExpiresAt: time.Time{}, TemporaryAdministrator: false,
		TemporaryExpiresAt: time.Time{}, Kind: identity.KindPerson,
		Credential: identity.CredentialAccessToken, JobID: "",
	}
}

func disableBreakGlass(t *testing.T, svc *principal.Service, id string) error {
	t.Helper()

	if err := svc.DisablePrincipal(t.Context(), testEntry(), siteAdministrator(), id); err != nil {
		return fmt.Errorf("disable principal %q: %w", id, err)
	}

	return nil
}

func deleteBreakGlass(t *testing.T, svc *principal.Service, id string) error {
	t.Helper()

	if err := svc.DeletePrincipal(t.Context(), testEntry(), siteAdministrator(), id); err != nil {
		return fmt.Errorf("delete principal %q: %w", id, err)
	}

	return nil
}

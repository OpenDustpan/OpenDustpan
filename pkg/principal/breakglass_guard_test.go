package principal_test

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/store/sqlite"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/principal"
)

// breakGlassChange is one covered write of a break-glass account.
type breakGlassChange func(t *testing.T, svc *principal.Service, id string) error

// breakGlassGuardCase is one row of the guard test: the accounts that exist,
// the write and the refusal that the row wants.
type breakGlassGuardCase struct {
	want     error
	name     string
	target   string
	change   breakGlassChange
	accounts []string
}

func TestBreakGlassGuard(t *testing.T) {
	t.Parallel()

	for _, tc := range []breakGlassGuardCase{
		{
			name: "disable the last enabled account is refused", change: disableBreakGlass,
			accounts: []string{"prn-bg1"}, target: "prn-bg1", want: principal.ErrLastBreakGlass,
		},
		{
			name: "delete the last enabled account is refused", change: deleteBreakGlass,
			accounts: []string{"prn-bg1"}, target: "prn-bg1", want: principal.ErrLastBreakGlass,
		},
		{
			name: "disable one of two enabled accounts passes", change: disableBreakGlass,
			accounts: []string{"prn-bg1", "prn-bg2"}, target: "prn-bg1", want: nil,
		},
		{
			name: "delete one of two enabled accounts passes", change: deleteBreakGlass,
			accounts: []string{"prn-bg1", "prn-bg2"}, target: "prn-bg1", want: nil,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			runBreakGlassGuardCase(t, tc)
		})
	}
}

// runBreakGlassGuardCase stores the accounts of the row, sends its change and
// checks the answer.
func runBreakGlassGuardCase(t *testing.T, tc breakGlassGuardCase) {
	t.Helper()

	svc := newBreakGlassService(t)

	for i, id := range tc.accounts {
		storeBreakGlass(t, svc.store, id, fmt.Sprintf("person-%d", i+1))
	}

	got := tc.change(t, svc.service, tc.target)
	if tc.want == nil {
		if got != nil {
			t.Fatalf("got %v, want nil", got)
		}

		return
	}

	if !errors.Is(got, tc.want) {
		t.Fatalf("got %v, want %v", got, tc.want)
	}

	// The older sentinel keeps its meaning: a change that leaves no enabled
	// account also leaves too few working accounts.
	if !errors.Is(got, principal.ErrNoWorkingBreakGlass) {
		t.Fatalf("got %v, want an error that matches %v", got, principal.ErrNoWorkingBreakGlass)
	}
}

// TestBreakGlassGuardRace proves that two disables of the last two enabled
// accounts cannot both pass: the store counts in the transaction of the
// write, so exactly one request is refused and one account stays enabled.
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

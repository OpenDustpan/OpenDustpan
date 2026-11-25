package principal_test

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/principal"
)

// TestBreakGlassAddAndReport proves that the service adds a break-glass
// account at zero working accounts, that it refuses a kind that is not a
// person, and that the report reads the live count and the floor.
func TestBreakGlassAddAndReport(t *testing.T) {
	t.Parallel()

	t.Run("add at zero accounts", func(t *testing.T) {
		t.Parallel()
		checkBreakGlassAddAtZero(t)
	})

	t.Run("report names the missing accounts", func(t *testing.T) {
		t.Parallel()
		checkBreakGlassReport(t)
	})
}

// checkBreakGlassAddAtZero adds the first account and proves its fields.
func checkBreakGlassAddAtZero(t *testing.T) {
	t.Helper()

	svc := newBreakGlassService(t)
	checkBreakGlassCount(t, svc, 0)

	account, err := svc.service.CreateBreakGlassAccount(t.Context(), testEntry(), principal.Input{
		Subject: "break-glass:recovery", DisplayName: "Recovery", Kind: principal.KindPerson,
	})
	if err != nil {
		t.Fatalf("add at zero accounts: %v", err)
	}

	if !account.BreakGlass || account.Kind != principal.KindPerson || account.Source != principal.SourceLocal ||
		account.Subject != "break-glass:recovery" || account.DisplayName != "Recovery" {
		t.Fatalf("account = %+v, want a local break-glass person", account)
	}

	stored, err := svc.store.GetPrincipal(t.Context(), account.ID)
	if err != nil || !stored.BreakGlass || stored.Subject != account.Subject {
		t.Fatalf("stored account = %+v, %v; want the break-glass person", stored, err)
	}

	// The account has no passkey yet, so it is not a working path.
	checkBreakGlassCount(t, svc, 0)
	checkBreakGlassRefusesService(t, svc)
}

// checkBreakGlassCount proves the live count of the service.
func checkBreakGlassCount(t *testing.T, svc breakGlassService, want int64) {
	t.Helper()

	count, err := svc.service.CountWorkingBreakGlass(t.Context(), svc.service.Now())
	if err != nil || count != want {
		t.Fatalf("working count = %d, %v; want %d", count, err, want)
	}
}

// checkBreakGlassRefusesService proves that the add refuses a service.
func checkBreakGlassRefusesService(t *testing.T, svc breakGlassService) {
	t.Helper()

	_, err := svc.service.CreateBreakGlassAccount(t.Context(), testEntry(), principal.Input{
		Subject: "break-glass:other", DisplayName: "", Kind: principal.KindService,
	})
	if !errors.Is(err, principal.ErrBreakGlassKind) {
		t.Fatalf("add of a service = %v, want ErrBreakGlassKind", err)
	}
}

// checkBreakGlassReport proves the count and the list of the accounts that
// exist without a working path.
func checkBreakGlassReport(t *testing.T) {
	t.Helper()

	svc := newBreakGlassService(t)
	storeBreakGlass(t, svc.store, "prn-bg1", "break-glass:one")
	storeBreakGlass(t, svc.store, "prn-bg2", "break-glass:two")

	count, err := svc.service.CountWorkingBreakGlass(t.Context(), svc.service.Now())
	if err != nil {
		t.Fatalf("count: %v", err)
	}

	// Neither account has a passkey or an administration grant.
	if count != 0 {
		t.Fatalf("working count = %d, want 0", count)
	}

	if principal.FloorWorkingPaths-count != 2 {
		t.Fatalf("missing count = %d, want 2", principal.FloorWorkingPaths-count)
	}

	accounts, err := svc.service.ListBreakGlassAccounts(t.Context())
	if err != nil || len(accounts) != 2 {
		t.Fatalf("accounts = %+v, %v; want the two break-glass accounts", accounts, err)
	}
}

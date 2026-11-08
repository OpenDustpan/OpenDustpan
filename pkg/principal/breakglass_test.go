package principal_test

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// storeBreakGlass stores an enabled break-glass person.
func storeBreakGlass(t *testing.T, st *sqlite.Store, id, subject string) {
	t.Helper()

	p := principal.Principal{
		CreatedAt: time.Unix(0, 0), UpdatedAt: time.Unix(0, 0), DisabledAt: time.Time{},
		ID: id, Subject: subject, DisplayName: subject, Kind: principal.KindPerson,
		Source: principal.SourceLocal, Namespace: "", GroupCount: 0, BreakGlass: true,
	}

	if err := st.CreatePrincipal(t.Context(), p, storetest.ServerChange(time.Unix(0, 0), "@server")); err != nil {
		t.Fatalf("create break-glass principal %q: %v", subject, err)
	}
}

// TestNoWorkingBreakGlassIsKept proves that the Service passes the refusal of the
// store for the last enabled break-glass account, in a disable and in a
// delete, and that the Guard answers first: a caller who fails the Guard gets
// the refusal of the Guard, not the state of the break-glass accounts.
func TestNoWorkingBreakGlassIsKept(t *testing.T) {
	t.Parallel()

	checks := 0
	changes := []groupCheck{}
	guard := fakeGuard{refuse: map[string]bool{}, checks: &checks, changes: &changes}
	st := openStore(t)

	svc, err := principal.NewService(principal.Config{
		TempAdmins: nil, Store: st, Guard: guard, Namespaces: nil, Tokens: nil, GroupReach: nil, Now: nil,
		NewID: nil,
	})
	if err != nil {
		t.Fatalf("service: %v", err)
	}

	storeBreakGlass(t, st, "prn-bg1", "first")
	storeBreakGlass(t, st, "prn-bg2", "second")

	ctx := t.Context()
	e := testEntry()

	var caller identity.Principal

	if disableErr := svc.DisablePrincipal(ctx, e, caller, "prn-bg1"); disableErr != nil {
		t.Fatalf("disable of one of two break-glass accounts: %v", disableErr)
	}

	disableErr := svc.DisablePrincipal(ctx, e, caller, "prn-bg2")
	if !errors.Is(disableErr, principal.ErrNoWorkingBreakGlass) {
		t.Fatalf("disable of the last break-glass account = %v, want ErrNoWorkingBreakGlass", disableErr)
	}

	deleteErr := svc.DeletePrincipal(ctx, e, caller, "prn-bg2")
	if !errors.Is(deleteErr, principal.ErrNoWorkingBreakGlass) {
		t.Fatalf("delete of the last break-glass account = %v, want ErrNoWorkingBreakGlass", deleteErr)
	}

	// A caller who lacks the grants of the target stops at the Guard.
	guard.refuse["prn-bg2"] = true

	disableErr = svc.DisablePrincipal(ctx, e, caller, "prn-bg2")
	if !errors.Is(disableErr, errRefused) {
		t.Fatalf("disable by a caller that fails the guard = %v, want the refusal of the guard", disableErr)
	}

	got, err := svc.GetPrincipal(ctx, "prn-bg2")
	if err != nil || !got.DisabledAt.IsZero() {
		t.Fatalf("last break-glass account after the refused requests = %+v, %v, want enabled", got, err)
	}
}

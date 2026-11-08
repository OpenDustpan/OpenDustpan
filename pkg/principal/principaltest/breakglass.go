package principaltest

import (
	"errors"
	"sync"
	"testing"

	"opendustpan.org/opendustpan/pkg/principal"
)

// breakGlassPerson stores an enabled break-glass person.
func breakGlassPerson(t *testing.T, s principal.Store, id, subject string) {
	t.Helper()

	p := newPrincipal(id, subject, principal.KindPerson)
	p.BreakGlass = true

	mustCreatePrincipal(t, s, p)
}

// testBreakGlassPasswordRefused states that a write that sets the
// break-glass flag on a person with a local password fails with
// principal.ErrBreakGlassPassword under the row lock of the principal. It
// skips while no write sets the break-glass flag on a person that exists:
// only the create of a principal sets it, so no person can hold a password
// and get the flag.
func testBreakGlassPasswordRefused(t *testing.T, s principal.Store) {
	t.Helper()

	if s == nil {
		t.Fatal("the case needs a store")
	}

	t.Skip("no port sets the break-glass flag on an existing person, so no break-glass person " +
		"holds a password; the local user store checks the flag in each write that would")
}

// wantEnabled fails the test unless the principal id exists and is enabled.
func wantEnabled(t *testing.T, s principal.Store, id string) {
	t.Helper()

	got, err := s.GetPrincipal(t.Context(), id)
	if err != nil || !got.DisabledAt.IsZero() {
		t.Fatalf("principal %q = %+v, %v; want present and enabled", id, got, err)
	}
}

// countEnabled returns how many of the principals ids exist and are enabled.
func countEnabled(t *testing.T, s principal.Store, ids ...string) int {
	t.Helper()

	n := 0

	for _, id := range ids {
		got, err := s.GetPrincipal(t.Context(), id)

		switch {
		case errors.Is(err, principal.ErrNotFound):
		case err != nil:
			t.Fatalf("get principal %q: %v", id, err)
		case got.DisabledAt.IsZero():
			n++
		default:
		}
	}

	return n
}

// testNoWorkingBreakGlassRefused proves that a disable and a delete of the last
// enabled break-glass account are refused and change nothing, and that the
// same writes work while a second enabled one remains.
func testNoWorkingBreakGlassRefused(t *testing.T, s principal.Store) {
	t.Helper()

	ctx := t.Context()

	breakGlassPerson(t, s, "prn-1", "first")
	breakGlassPerson(t, s, "prn-2", "second")

	if err := s.DisablePrincipal(ctx, "prn-1", entry()); err != nil {
		t.Fatalf("disable of one of two break-glass accounts: %v", err)
	}

	wantErr(t, "disable of the last break-glass account", s.DisablePrincipal(ctx, "prn-2", entry()),
		principal.ErrNoWorkingBreakGlass)
	wantErr(t, "delete of the last break-glass account", s.DeletePrincipal(ctx, "prn-2", entry()),
		principal.ErrNoWorkingBreakGlass)

	// Each refused write left the account as it was.
	wantEnabled(t, s, "prn-2")

	// A disabled account does not count, so the account that it left can
	// go once the disabled one is enabled again.
	if err := s.EnablePrincipal(ctx, "prn-1", entry()); err != nil {
		t.Fatalf("enable: %v", err)
	}

	if err := s.DeletePrincipal(ctx, "prn-2", entry()); err != nil {
		t.Fatalf("delete of one of two break-glass accounts: %v", err)
	}

	wantErr(t, "disable of the one break-glass account that is left", s.DisablePrincipal(ctx, "prn-1", entry()),
		principal.ErrNoWorkingBreakGlass)
}

// testBreakGlassDisabledOrEndedDoNotCount proves that only an enabled
// break-glass account counts as a second one, and that the delete of a
// disabled break-glass account is not the delete of the last enabled one.
func testBreakGlassDisabledOrEndedDoNotCount(t *testing.T, s principal.Store) {
	t.Helper()

	ctx := t.Context()

	breakGlassPerson(t, s, "prn-1", "enabled")
	breakGlassPerson(t, s, "prn-2", "disabled")
	breakGlassPerson(t, s, "prn-3", "ended")

	if err := s.DeletePrincipal(ctx, "prn-3", entry()); err != nil {
		t.Fatalf("delete of one of three break-glass accounts: %v", err)
	}

	if err := s.DisablePrincipal(ctx, "prn-2", entry()); err != nil {
		t.Fatalf("disable of one of two break-glass accounts: %v", err)
	}

	wantErr(t, "disable with a disabled and an ended account left", s.DisablePrincipal(ctx, "prn-1", entry()),
		principal.ErrNoWorkingBreakGlass)
	wantErr(t, "delete with a disabled and an ended account left", s.DeletePrincipal(ctx, "prn-1", entry()),
		principal.ErrNoWorkingBreakGlass)

	// The delete of a disabled account takes no enabled one.
	if err := s.DeletePrincipal(ctx, "prn-2", entry()); err != nil {
		t.Fatalf("delete of a disabled break-glass account: %v", err)
	}

	wantEnabled(t, s, "prn-1")
}

// testBreakGlassRuleIsForBreakGlassOnly proves that the rule does not touch
// a principal that is not a break-glass account, with no break-glass
// account in the installation and with one.
func testBreakGlassRuleIsForBreakGlassOnly(t *testing.T, s principal.Store) {
	t.Helper()

	ctx := t.Context()

	mustCreatePrincipal(t, s, newPrincipal("prn-1", "alice", principal.KindPerson))
	mustCreatePrincipal(t, s, newPrincipal("prn-2", "bob", principal.KindPerson))
	breakGlassPerson(t, s, "prn-3", "only")

	if err := s.DisablePrincipal(ctx, "prn-1", entry()); err != nil {
		t.Fatalf("disable of a person that is not a break-glass account: %v", err)
	}

	if err := s.DeletePrincipal(ctx, "prn-2", entry()); err != nil {
		t.Fatalf("delete of a person that is not a break-glass account: %v", err)
	}

	wantEnabled(t, s, "prn-3")
}

// WriteHolder is an optional capability of a store for the parallel cases of
// the break-glass rule. A store that runs one write at a time does not
// implement it. storetest.AuditWriteHolder has the same methods.
type WriteHolder interface {
	// HoldAuditWrites makes each audit append of the store wait until release
	// runs. Reads of the store do not wait. release can run more than once.
	HoldAuditWrites(t *testing.T) (release func())
	// WaitForLockWaiters returns when n transactions of the store wait for a
	// lock.
	WaitForLockWaiters(t *testing.T, n int)
}

// runParallel runs the two writes at the same time and returns their errors.
//
// When the store is a WriteHolder, the case holds the audit appends until both
// writes wait for a lock. A write appends its audit entry after its statement.
// So when the store does not serialize the two writes, both statements see the
// other account as enabled before either one commits, and both succeed: the
// case fails every time, not only on a lucky interleaving. When the store
// serializes them, one write waits for the lock of the other and the case
// passes.
func runParallel(t *testing.T, s principal.Store, writes [2]func() error) []error {
	t.Helper()

	var (
		wg      sync.WaitGroup
		results [2]error
		start   = make(chan struct{})
	)

	release := func() {}

	holder, holds := s.(WriteHolder)
	if holds {
		release = holder.HoldAuditWrites(t)
	}

	for i := range writes {
		wg.Go(func() {
			<-start

			results[i] = writes[i]()
		})
	}

	close(start)

	if holds {
		holder.WaitForLockWaiters(t, len(writes))
	}

	release()
	wg.Wait()

	return results[:]
}

// testBreakGlassParallel proves that two writes at the same time that each
// take one of the last two enabled break-glass accounts leave one enabled:
// exactly one write is refused.
func testBreakGlassParallel(t *testing.T, s principal.Store) {
	t.Helper()

	breakGlassPerson(t, s, "prn-1", "first")
	breakGlassPerson(t, s, "prn-2", "second")

	results := runParallel(t, s, [2]func() error{
		func() error { return s.DisablePrincipal(t.Context(), "prn-1", entry()) },
		func() error { return s.DisablePrincipal(t.Context(), "prn-2", entry()) },
	})

	assertOneRefused(t, "parallel disables of the last two break-glass accounts", results)

	if got := countEnabled(t, s, "prn-1", "prn-2"); got != 1 {
		t.Fatalf("enabled break-glass accounts after the parallel disables = %d, want 1", got)
	}
}

// testBreakGlassParallelDelete is testBreakGlassParallel for a disable of one
// account at the same time as a delete of the other.
func testBreakGlassParallelDelete(t *testing.T, s principal.Store) {
	t.Helper()

	breakGlassPerson(t, s, "prn-1", "first")
	breakGlassPerson(t, s, "prn-2", "second")

	results := runParallel(t, s, [2]func() error{
		func() error { return s.DisablePrincipal(t.Context(), "prn-1", entry()) },
		func() error { return s.DeletePrincipal(t.Context(), "prn-2", entry()) },
	})

	assertOneRefused(t, "a disable and a delete of the last two break-glass accounts", results)

	if got := countEnabled(t, s, "prn-1", "prn-2"); got != 1 {
		t.Fatalf("enabled break-glass accounts after the parallel writes = %d, want 1", got)
	}
}

// assertOneRefused fails the test unless exactly one of results is
// principal.ErrNoWorkingBreakGlass and the others are nil.
func assertOneRefused(t *testing.T, what string, results []error) {
	t.Helper()

	refused := 0

	for _, err := range results {
		switch {
		case err == nil:
		case errors.Is(err, principal.ErrNoWorkingBreakGlass):
			refused++
		default:
			t.Fatalf("%s = %v, want nil or ErrNoWorkingBreakGlass", what, err)
		}
	}

	if refused != 1 {
		t.Fatalf("%s: %d refused (%v), want exactly one", what, refused, results)
	}
}

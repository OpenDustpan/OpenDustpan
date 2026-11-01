package sessiontest

import (
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/session"
)

// StateStore is what the suite of session.PrincipalStateStore needs: the
// port, and the stores that make its principals and read its audit log.
type StateStore interface {
	Store
	session.PrincipalStateStore
	auditLister
}

// StateFactory returns a fresh, empty store for one case.
type StateFactory func(t *testing.T) StateStore

// concurrentBumps is the count of bumps at the same time.
const concurrentBumps = 12

// stateCase is one case of the suite.
type stateCase struct {
	test func(t *testing.T, s StateStore)
	name string
}

// stateCases returns the cases of the suite, in the order of the run.
func stateCases() []stateCase {
	return []stateCase{
		{name: "AnUnknownPrincipalIsNotFound", test: testStateUnknown},
		{name: "ADeletedPrincipalIsNotFound", test: testStateDeleted},
		{name: "AServicePrincipalIsNotFound", test: testStateService},
		{name: "ADisabledPrincipalComesBackWithItsDisableTime", test: testStateDisabled},
		{name: "ANewPrincipalHasGenerationOne", test: testStateNew},
		{name: "TheStateHoldsThePoliciesFromTheSameRead", test: testStatePolicies},
		{name: "BumpAddsOne", test: testBumpAddsOne},
		{name: "BumpReturnsTheNewGeneration", test: testBumpReturns},
		{name: "BumpNeedsNoClock", test: testBumpNoClock},
		{name: "AZeroGenerationFromAnotherVersionIsRefused", test: testStateZero},
		{name: "BumpOfAnUnknownCauseIsInvalid", test: testBumpUnknownCause},
		{name: "BumpOfAnUnknownPrincipalIsNotFound", test: testBumpUnknownPrincipal},
		{name: "BumpWithABadAuditEntryChangesNothing", test: testBumpBadEntry},
		{name: "BumpAppendsTheEntryInTheSameTransaction", test: testBumpAppends},
		{name: "ConcurrentBumpsEachAddOne", test: testBumpConcurrent},
	}
}

// RunPrincipalStateStore runs the conformance suite of a
// session.PrincipalStateStore against stores from newStore.
func RunPrincipalStateStore(t *testing.T, newStore StateFactory) {
	t.Helper()

	for _, c := range stateCases() {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if newStore == nil {
				t.Fatal("sessiontest.RunPrincipalStateStore needs a store factory")
			}

			c.test(t, newStore(t))
		})
	}
}

// bumpEntry returns a valid entry of a bump of the principal id.
func bumpEntry(id string, when time.Time) audit.Entry {
	e := entry("ses-x", when)
	e.Action = session.ActionGenerationBumped
	e.Object = principal.Ref(id)

	return e
}

// bumpOne bumps the generation of the person prn-1 with the cause password
// and fails on an error.
func bumpOne(t *testing.T, s StateStore) uint64 {
	t.Helper()

	g, err := s.BumpGeneration(t.Context(), "prn-1", session.BumpPassword, bumpEntry("prn-1", at(1)))
	if err != nil {
		t.Fatalf("bump prn-1: %v", err)
	}

	return g
}

// stateOf returns the state of id and fails on an error.
func stateOf(t *testing.T, s StateStore, id string) session.PrincipalState {
	t.Helper()

	st, err := s.PrincipalState(t.Context(), id)
	if err != nil {
		t.Fatalf("state of %s: %v", id, err)
	}

	return st
}

// bumpEntries returns the entries of bumps in the audit log of s.
func bumpEntries(t *testing.T, s StateStore) []audit.Entry {
	t.Helper()

	listing, err := s.List(t.Context(), audit.ServerScope, audit.Page{Number: 1, Size: audit.DefaultPageSize})
	if err != nil {
		t.Fatalf("list entries: %v", err)
	}

	var out []audit.Entry

	for i := range listing.Entries {
		if listing.Entries[i].Action == session.ActionGenerationBumped {
			out = append(out, listing.Entries[i])
		}
	}

	return out
}

func testStateUnknown(t *testing.T, s StateStore) {
	t.Helper()

	_, err := s.PrincipalState(t.Context(), "prn-missing")
	wantErr(t, "state of an unknown principal", err, session.ErrPrincipalNotFound)
}

func testStateDeleted(t *testing.T, s StateStore) {
	t.Helper()

	mustPerson(t, s, "prn-gone")

	e := entry("ses-x", at(1))
	e.Object = principal.Ref("prn-gone")

	if err := s.DeletePrincipal(t.Context(), "prn-gone", e); err != nil {
		t.Fatalf("delete principal: %v", err)
	}

	_, err := s.PrincipalState(t.Context(), "prn-gone")
	wantErr(t, "state of a deleted principal", err, session.ErrPrincipalNotFound)
}

func testStateService(t *testing.T, s StateStore) {
	t.Helper()

	mustPrincipalAt(t, s, "prn-svc", principal.KindService, at(0))

	_, err := s.PrincipalState(t.Context(), "prn-svc")
	wantErr(t, "state of a service principal", err, session.ErrPrincipalNotFound)

	_, err = s.BumpGeneration(t.Context(), "prn-svc", session.BumpPassword, bumpEntry("prn-svc", at(1)))
	wantErr(t, "bump of a service principal", err, session.ErrPrincipalNotFound)
}

func testStateDisabled(t *testing.T, s StateStore) {
	t.Helper()

	mustPerson(t, s, "prn-1")

	e := entry("ses-x", at(3))
	e.Object = principal.Ref("prn-1")

	if err := s.DisablePrincipal(t.Context(), "prn-1", e); err != nil {
		t.Fatalf("disable principal: %v", err)
	}

	st := stateOf(t, s, "prn-1")
	if st.Owner.DisabledAt.IsZero() || st.Owner.ID != "prn-1" || st.Generation != 1 {
		t.Fatalf("state of a disabled person = %+v, want its disable time and the generation 1", st)
	}
}

func testStateNew(t *testing.T, s StateStore) {
	t.Helper()

	mustPerson(t, s, "prn-1")

	st := stateOf(t, s, "prn-1")
	if st.Generation != 1 || st.Owner.Kind != principal.KindPerson || st.Owner.ID != "prn-1" {
		t.Fatalf("state of a new person = %+v, want the generation 1", st)
	}
}

func testStatePolicies(t *testing.T, s StateStore) {
	t.Helper()

	mustPerson(t, s, "prn-1")
	setAccounts(t, s, true, 2*time.Hour, at(2))

	want, err := s.GetSessionPolicy(t.Context())
	if err != nil {
		t.Fatalf("session policy: %v", err)
	}

	st := stateOf(t, s, "prn-1")
	if st.Policy != want || !st.Accounts.DisableAccounts || st.Accounts.Inactivity != 2*time.Hour {
		t.Fatalf("state = %+v, want the policy %+v and the account disable after 2h", st, want)
	}
}

func testBumpAddsOne(t *testing.T, s StateStore) {
	t.Helper()

	mustPerson(t, s, "prn-1")
	mustPerson(t, s, "prn-2")

	bumpOne(t, s)
	bumpOne(t, s)

	if got := stateOf(t, s, "prn-1").Generation; got != 3 {
		t.Fatalf("generation after two bumps = %d, want 3", got)
	}

	if got := stateOf(t, s, "prn-2").Generation; got != 1 {
		t.Fatalf("generation of another person = %d, want 1", got)
	}
}

func testBumpReturns(t *testing.T, s StateStore) {
	t.Helper()

	mustPerson(t, s, "prn-1")

	if g := bumpOne(t, s); g != 2 || stateOf(t, s, "prn-1").Generation != 2 {
		t.Fatalf("bump returned %d, want 2 and the stored 2", g)
	}
}

func testBumpNoClock(t *testing.T, s StateStore) {
	t.Helper()

	mustPerson(t, s, "prn-1")

	// The entries of the bumps go back in time: a bump reads no clock, so
	// each one still adds 1.
	for i := 3; i > 0; i-- {
		_, err := s.BumpGeneration(t.Context(), "prn-1", session.BumpAdministrator, bumpEntry("prn-1", at(i)))
		if err != nil {
			t.Fatalf("bump at minute %d: %v", i, err)
		}
	}

	if got := stateOf(t, s, "prn-1").Generation; got != 4 {
		t.Fatalf("generation = %d, want 4", got)
	}
}

func testStateZero(t *testing.T, s StateStore) {
	t.Helper()

	mustPerson(t, s, "prn-1")

	// A session from before the generation existed carries 0, and the state
	// never answers 0: the session cannot match a principal.
	if st := stateOf(t, s, "prn-1"); st.Generation == 0 {
		t.Fatalf("state = %+v, want a generation of at least 1", st)
	}

	ss := newSession("ses-1", "prn-1", at(5))
	mustSession(t, s, ss, digest(1))

	m, err := s.GetSessionByDigest(t.Context(), digest(1))
	if err != nil || m.Generation == 0 {
		t.Fatalf("lookup = %+v, %v; want a stored generation of at least 1", m, err)
	}
}

func testBumpUnknownCause(t *testing.T, s StateStore) {
	t.Helper()

	mustPerson(t, s, "prn-1")

	for _, cause := range []session.BumpCause{"", "PASSWORD", "password\n", "reboot"} {
		_, err := s.BumpGeneration(t.Context(), "prn-1", cause, bumpEntry("prn-1", at(1)))
		wantErr(t, "bump with the cause "+strconv.Quote(string(cause)), err, session.ErrInvalid)
	}

	if got := stateOf(t, s, "prn-1").Generation; got != 1 || len(bumpEntries(t, s)) != 0 {
		t.Fatalf("generation after refused bumps = %d, want 1 and no entry", got)
	}
}

func testBumpUnknownPrincipal(t *testing.T, s StateStore) {
	t.Helper()

	_, err := s.BumpGeneration(t.Context(), "prn-missing", session.BumpPassword, bumpEntry("prn-missing", at(1)))
	wantErr(t, "bump of an unknown principal", err, session.ErrPrincipalNotFound)

	if len(bumpEntries(t, s)) != 0 {
		t.Fatal("a refused bump appended an entry")
	}
}

func testBumpBadEntry(t *testing.T, s StateStore) {
	t.Helper()

	mustPerson(t, s, "prn-1")

	bad := bumpEntry("prn-1", at(1))
	bad.Subject = ""

	_, err := s.BumpGeneration(t.Context(), "prn-1", session.BumpPassword, bad)
	wantErr(t, "bump with a bad entry", err, audit.ErrInvalidEntry)

	if got := stateOf(t, s, "prn-1").Generation; got != 1 || len(bumpEntries(t, s)) != 0 {
		t.Fatalf("generation after a refused bump = %d, want 1 and no entry", got)
	}
}

func testBumpAppends(t *testing.T, s StateStore) {
	t.Helper()

	mustPerson(t, s, "prn-1")
	bumpOne(t, s)

	got := bumpEntries(t, s)
	if len(got) != 1 || got[0].Object != principal.Ref("prn-1") {
		t.Fatalf("bump entries = %+v, want one of prn-1", got)
	}

	changes := map[string]string{}
	for _, c := range got[0].Changes {
		changes[c.Key] = c.New
	}

	if changes["cause"] != string(session.BumpPassword) || changes["generation"] != "2" {
		t.Fatalf("changes of the bump = %v, want the cause password and the generation 2", changes)
	}
}

func testBumpConcurrent(t *testing.T, s StateStore) {
	t.Helper()

	mustPerson(t, s, "prn-1")

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		seen = map[uint64]bool{}
		errs []error
	)

	for range concurrentBumps {
		wg.Go(func() {
			g, err := s.BumpGeneration(t.Context(), "prn-1", session.BumpPassword, bumpEntry("prn-1", at(1)))

			mu.Lock()
			defer mu.Unlock()

			if err != nil {
				errs = append(errs, err)

				return
			}

			seen[g] = true
		})
	}

	wg.Wait()

	if err := errors.Join(errs...); err != nil {
		t.Fatalf("a concurrent bump failed: %v", err)
	}

	if got := stateOf(t, s, "prn-1").Generation; got != 1+concurrentBumps || len(seen) != concurrentBumps {
		t.Fatalf("generation = %d with %d distinct answers, want %d and %d", got, len(seen), 1+concurrentBumps,
			concurrentBumps)
	}
}

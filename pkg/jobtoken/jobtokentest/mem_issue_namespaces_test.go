package jobtokentest_test

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/jobtoken"
	"opendustpan.org/opendustpan/pkg/jobtoken/jobtokentest"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// namespaceIDOf returns the stable ID of the namespace ns in the world of the
// fixture.
func namespaceIDOf(t *testing.T, f *fixture, ns namespace.Name) string {
	t.Helper()

	f.world.mu.Lock()
	defer f.world.mu.Unlock()

	id, ok := f.world.nsByName[ns]
	if !ok {
		t.Fatalf("namespace %s has no record", ns)
	}

	return id
}

// emptyUnitNamespace returns a token of the fixture and an empty namespace
// ID, with the namespace row of the unit deleted, so that both sides of the
// unit comparison are empty.
func emptyUnitNamespace(t *testing.T, f *fixture) (jobtoken.Token, string) {
	t.Helper()

	f.h.DeleteNamespace(t, f.fence.Namespace)

	tok, _ := liveRaw(t, f, f.alice, memExpiry())

	return tok, ""
}

// emptyJobNamespace returns a token of the fixture and an empty namespace ID,
// with the namespace ID of the job removed from the map of job namespaces, so
// that both sides of the job comparison are empty.
func emptyJobNamespace(t *testing.T, f *fixture) (jobtoken.Token, string) {
	t.Helper()

	f.world.mu.Lock()
	delete(f.world.jobNamespaces, f.job.ID)
	f.world.mu.Unlock()

	tok, _ := liveRaw(t, f, f.alice, memExpiry())

	return tok, ""
}

// emptyBothNamespaces returns a token of the fixture and an empty namespace
// ID, with the namespace row of the unit deleted and the namespace ID of the
// job removed from the map of job namespaces, so that both sides of both
// comparisons are empty.
func emptyBothNamespaces(t *testing.T, f *fixture) (jobtoken.Token, string) {
	t.Helper()

	f.h.DeleteNamespace(t, f.fence.Namespace)

	f.world.mu.Lock()
	delete(f.world.jobNamespaces, f.job.ID)
	f.world.mu.Unlock()

	tok, _ := liveRaw(t, f, f.alice, memExpiry())

	return tok, ""
}

// foreignUnit returns a token of a unit in another namespace, with the
// namespace ID of the namespace of the fixture, so that only the unit guard
// refuses.
func foreignUnit(t *testing.T, f *fixture) (jobtoken.Token, string) {
	t.Helper()

	f.h.Namespace(t, "globex")
	unit := f.h.Unit(t, "globex", "pulumi", "web/other")

	tok, _ := rawToken(t, &rawSpec{
		unit: unit, starter: f.alice, jobID: f.job.ID, operation: f.op, expiresAt: memExpiry(),
	})

	return tok, namespaceIDOf(t, f, f.fence.Namespace)
}

// foreignJob returns a token whose job is in another namespace, with the
// namespace ID of the namespace of the fixture, so that only the job guard
// refuses.
func foreignJob(t *testing.T, f *fixture) (jobtoken.Token, string) {
	t.Helper()

	f.h.Namespace(t, "globex")
	unit := f.h.Unit(t, "globex", "pulumi", "web/other")
	op := jobtokentest.Operation{
		ID: durable.ID("op-b"), State: durable.StateRunning, Token: 1, LeaseEnd: memNow().Add(memLease),
	}
	foreign := f.h.Job(t, unit, f.alice, op)

	tok, _ := rawToken(t, &rawSpec{
		unit: f.unitA, starter: f.alice, jobID: foreign.ID, operation: f.op, expiresAt: memExpiry(),
	})

	return tok, namespaceIDOf(t, f, f.fence.Namespace)
}

// liveNamespaces returns a valid token of the fixture and the namespace ID of
// its namespace.
func liveNamespaces(t *testing.T, f *fixture) (jobtoken.Token, string) {
	t.Helper()

	tok, _ := liveRaw(t, f, f.alice, memExpiry())

	return tok, namespaceIDOf(t, f, f.fence.Namespace)
}

// TestCheckIssueNamespacesRefusesEmptyAndForeignNamespaceIDs sends worlds
// straight to checkIssueNamespaces of the memory double, so that only the
// namespace guards decide. Each hostile case wants ErrNotLive. The last case
// proves that the world is otherwise valid: it passes.
func TestCheckIssueNamespacesRefusesEmptyAndForeignNamespaceIDs(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		setup   func(t *testing.T, f *fixture) (jobtoken.Token, string)
		refused bool
	}{
		{"an empty namespace ID on both sides of the unit comparison", emptyUnitNamespace, true},
		{"an empty namespace ID on both sides of the job comparison", emptyJobNamespace, true},
		{"an empty namespace ID on both sides of both comparisons", emptyBothNamespaces, true},
		{"a unit row in another namespace", foreignUnit, true},
		{"a job in another namespace", foreignJob, true},
		{"a live unit row and a live job in the namespace of the token", liveNamespaces, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)
			tok, namespaceID := tc.setup(t, f)

			f.world.mu.Lock()
			defer f.world.mu.Unlock()

			err := f.world.checkIssueNamespaces(tok, namespaceID)

			if tc.refused {
				if !errors.Is(err, jobtoken.ErrNotLive) {
					t.Fatalf("%s: checkIssueNamespaces = %v, want ErrNotLive", tc.name, err)
				}

				return
			}

			if err != nil {
				t.Fatalf("%s: checkIssueNamespaces = %v, want nil", tc.name, err)
			}
		})
	}
}

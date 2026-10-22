// Package drifttest is the conformance suite of the ports of package drift:
// Store, PolicyStore and Checker. A new adapter runs Run with a Factory.
//
// The cases for Store and PolicyStore are complete and run on the memory
// reference in this package's tests. The cases for Checker need a job service
// and wait for the first slice of drift detection. The cases for the inventory
// projection, the notification and the audit entry run when the harness gives
// the hook that reads them.
package drifttest

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/drift"
	"opendustpan.org/opendustpan/pkg/graph"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Jobs makes the jobs that Store.Record checks. Each function makes a job of
// unit in the store of the adapter and returns its ID. Every job starts at the
// start of JobWindow. A job that ended, ended at the end of JobWindow.
type Jobs struct {
	// DriftCheck makes a drift check job (operation refresh, purpose drift
	// check) that succeeded.
	DriftCheck func(t *testing.T, unit graph.Unit) job.ID
	// Running makes a drift check job that has not ended.
	Running func(t *testing.T, unit graph.Unit) job.ID
	// Failed makes a drift check job that ended in the state failed.
	Failed func(t *testing.T, unit graph.Unit) job.ID
	// Canceled makes a drift check job that ended in the state canceled.
	Canceled func(t *testing.T, unit graph.Unit) job.ID
	// Plain makes a job without the purpose drift check that succeeded, for
	// example a plan job.
	Plain func(t *testing.T, unit graph.Unit) job.ID
}

// Window is a span of time.
type Window struct {
	Start, End time.Time
}

// JobWindow returns the start and the end of the jobs that the harness makes.
// The results of the fixtures are checked inside this window.
func JobWindow() Window {
	return Window{Start: at(0), End: at(minuteJobEnd)}
}

// Harness is one fresh, empty store for one subtest. Namespace makes the
// namespace name and returns its current fence. Projection, Notified and
// Audited are optional hooks: a case that needs one skips when it is nil.
// Projection reads the drift status that the inventory projection holds for
// unit, or drift.StatusUnknown when it holds none. Notified returns the dedupe
// keys of the drift notifications in the outbox of the namespace. Audited
// returns the number of audit entries of the namespace.
type Harness struct {
	Store      drift.Store
	Policies   drift.PolicyStore
	Checker    drift.Checker
	Namespace  func(t *testing.T, name string) namespace.Fence
	Jobs       Jobs
	Projection func(t *testing.T, unit graph.Unit) drift.Status
	Notified   func(t *testing.T, ns string) []string
	Audited    func(t *testing.T, ns string) int
}

// Factory returns a fresh harness for one subtest.
type Factory func(t *testing.T) Harness

type suiteCase struct {
	name string
	test func(t *testing.T, h Harness)
}

// Run runs every case of the suite. A case of PolicyStore skips when the
// harness has no PolicyStore, and a case of Checker skips when it has no
// Checker, so an adapter of Store alone runs the cases that it can.
func Run(t *testing.T, newHarness Factory) {
	t.Helper()

	groups := []struct {
		cases   []suiteCase
		port    string
		missing func(h Harness) bool
	}{
		{storeCases(), "Store", func(h Harness) bool { return h.Store == nil }},
		{policyCases(), "PolicyStore", func(h Harness) bool { return h.Policies == nil }},
		{checkerCases(), "Checker", func(h Harness) bool { return h.Checker == nil }},
	}

	for _, g := range groups {
		for _, tc := range g.cases {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				h := newHarness(t)
				if g.missing(h) {
					t.Skip("the harness has no " + g.port)
				}

				tc.test(t, h)
			})
		}
	}
}

// The namespace of the fixtures.
const acme namespace.Name = "acme"

// The minutes of the checks in the fixtures, and the epoch of at.
const (
	minute2 = 2
	minute3 = 3
	minute4 = 4
	minute5 = 5
	// minuteJobEnd is the minute at which the ended jobs of the harness end.
	minuteJobEnd = 10
	minuteLate   = 11
	fixtureT     = 1_800_000_000_000
)

// at returns the time of the fixtures, plus n minutes.
func at(n int) time.Time {
	return time.UnixMilli(fixtureT).UTC().Add(time.Duration(n) * time.Minute)
}

// unit returns a valid unit of namespace ns with ID id.
func unit(ns, id string) graph.Unit {
	return graph.Unit{Namespace: namespace.Name(ns), Tool: "opentofu", ID: id}
}

// stale returns a fence that does not match the one the store holds.
func stale(f namespace.Fence) namespace.Fence {
	return namespace.Fence{Namespace: f.Namespace, FailoverVersion: f.FailoverVersion + 1}
}

// result returns a result of u for the job id, checked at minute n, that lists
// the given addresses as changed.
func result(u graph.Unit, id job.ID, n int, addresses ...string) drift.Result {
	resources := make([]drift.Resource, 0, len(addresses))
	for _, a := range addresses {
		resources = append(resources, drift.Resource{Address: a, Kind: drift.KindChanged})
	}

	return drift.Result{CheckedAt: at(n), Unit: u, JobID: id, Resources: resources, Truncated: false}
}

func wantErr(t *testing.T, what string, err, want error) {
	t.Helper()

	if !errors.Is(err, want) {
		t.Errorf("%s: %v, want %v", what, err, want)
	}
}

// wantRefused checks that a refused Record returned want and the zero
// transition, so that no caller can read a result from a refusal.
func wantRefused(t *testing.T, what string, tr drift.Transition, err, want error) {
	t.Helper()

	wantErr(t, what, err, want)

	if tr != (drift.Transition{}) {
		t.Errorf("%s: transition = %+v, want the zero transition", what, tr)
	}
}

func must(t *testing.T, what string, err error) {
	t.Helper()

	if err != nil {
		t.Fatalf("%s: %v", what, err)
	}
}

func auditOf(action string) audit.Entry {
	return audit.Entry{
		At:            at(0),
		Namespace:     acme,
		Subject:       "user:ada",
		Action:        action,
		IDPSubject:    "",
		TokenID:       "",
		Object:        "",
		Version:       "",
		ValuePath:     "",
		ValueCount:    nil,
		Changes:       nil,
		SourceIP:      "",
		UserAgent:     "",
		Channel:       "",
		Kind:          "",
		Outcome:       "",
		RequestID:     "",
		ObjectID:      "",
		PrevHash:      "",
		Hash:          "",
		Sequence:      0,
		FenceVersion:  0,
		Reason:        "",
		PrincipalKind: "",
		JobID:         "",
	}
}

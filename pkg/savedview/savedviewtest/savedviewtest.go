// Package savedviewtest is the conformance suite of the Store port of package
// savedview. A new adapter runs Run with a Factory.
//
// The suite is a skeleton: each case has its name and its rule in the comment,
// and skips until the first adapter exists. The cases are written before the
// adapter, so that the adapter has a target.
package savedviewtest

import (
	"slices"
	"testing"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/savedview"
)

// Harness is one fresh, empty store for one subtest. Namespace makes the
// namespace called name and returns its current fence. Audited is an optional
// hook that returns the number of audit entries of the namespace: a case that
// needs it skips when it is nil.
type Harness struct {
	Store     savedview.Store
	Namespace func(t *testing.T, name string) namespace.Fence
	Audited   func(t *testing.T, ns string) int
}

// Factory returns a fresh harness for one subtest.
type Factory func(t *testing.T) Harness

type suiteCase struct {
	name string
	test func(t *testing.T, h Harness)
}

// CaseNames returns the name of each case, in order.
func CaseNames() []string {
	cases := cases()
	names := make([]string, 0, len(cases))

	for _, tc := range cases {
		names = append(names, tc.name)
	}

	return names
}

// Run runs every case of the suite.
func Run(t *testing.T, newHarness Factory) {
	t.Helper()

	for _, tc := range cases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Skip before the harness is built: a skipped case must not create
			// a database.
			if tc.test == nil {
				t.Skip("not implemented: waits for the first adapter")
			}

			tc.test(t, newHarness(t))
		})
	}
}

// pending is a case with a name and no test yet. Run skips it.
func pending(name string) suiteCase {
	return suiteCase{name: name, test: nil}
}

// cases lists the rules of the Store port. Each name states one rule.
func cases() []suiteCase {
	return slices.Concat(
		// Create stores the view, and Get returns the same name, owner and
		// query. The query holds no result.
		createCases(),
		// A view is visible to its owner only. This is the defense layer of
		// the store: the handler checks the owner too.
		visibilityCases(),
		// A write carries the fence and the audit entry of the change, and a
		// failure changes nothing.
		writeCases(),
	)
}

func createCases() []suiteCase {
	return []suiteCase{
		pending("create then get returns the same view"),
		pending("create refuses a view that fails validation"),
		pending("create refuses a second view with the same name for the same owner"),
		pending("create accepts the same name for another owner"),
		pending("create accepts the same name in another namespace"),
		pending("create refuses the view after the largest number of views of one owner"),
		pending("create at the namespace cap returns ErrLimit, also for an owner with no view"),
		pending("create accepts a query with a repeated label and a repeated reference, as the unit list does"),
		pending("list is ordered by name, then ID"),
		pending("list returns an empty list for an owner with no view"),
		pending("a query round trip keeps the order of its labels and references"),
	}
}

func visibilityCases() []suiteCase {
	return []suiteCase{
		pending("get of the view of another owner returns not found"),
		pending("get of an unknown ID returns the same error as a view of another owner"),
		pending("get of a view in another namespace returns not found"),
		pending("list returns the views of the owner only"),
		pending("views of a deleted principal are gone, and a new principal with the same subject sees none"),
		pending("delete of the view of another owner returns not found and keeps the view"),
	}
}

func writeCases() []suiteCase {
	return []suiteCase{
		pending("create with a stale fence returns ErrFenced and stores nothing"),
		pending("create with the fence of another namespace returns ErrFenced"),
		pending("delete with a stale fence returns ErrFenced and keeps the view"),
		pending("create appends one audit entry in the same transaction"),
		pending("a create that fails appends no audit entry"),
		pending("delete appends one audit entry in the same transaction"),
		pending("delete of an unknown view returns not found and appends no audit entry"),
		pending("concurrent creates of one name store one view"),
		pending("concurrent creates at the limit store no more than the limit"),
		pending("delete of a principal deletes its views and audits each view by ID only"),
	}
}

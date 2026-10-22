// Package outputsharetest is the conformance suite of the ports of package
// outputshare: Store, NewUnitRules and Checker. A new adapter runs Run with a
// Factory.
//
// No adapter implements the ports yet. Each case names one behavior and is
// skipped with the reason Pending until the slice that adds the first adapter
// writes it. That slice writes the case before the adapter code, and removes
// the skip of each case that it writes.
package outputsharetest

import (
	"slices"
	"testing"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/outputshare"
)

// Pending is the skip reason of a case that no adapter can run yet.
const Pending = "no adapter implements the output sharing ports yet; " +
	"the slice that adds the first adapter writes this case"

// Harness is one fresh, empty set of ports for one subtest.
type Harness struct {
	// Store keeps the rules and the setting.
	Store outputshare.Store
	// NewUnitRules gives the rule of the setting to a new unit.
	NewUnitRules outputshare.NewUnitRules
	// Checker decides each read.
	Checker outputshare.Checker
	// Namespace makes the namespace name and returns its current fence.
	Namespace func(t *testing.T, name string) namespace.Fence
	// Unit makes a unit of the tool in the namespace, in the collection when
	// it is not empty, and returns its placement with the stable IDs.
	Unit func(t *testing.T, tool string, ns namespace.Name, name, collection string) outputshare.Placement
	// Grant gives the principal the action on the object.
	Grant func(t *testing.T, principal string, action identity.Action, object identity.Object)
	// Job starts a job of the unit for the starter principal and returns the
	// principal of its job token.
	Job func(t *testing.T, unit outputshare.Placement, starter string) identity.Principal
	// Disable disables the principal, as a person who leaves the team.
	Disable func(t *testing.T, principal string)
	// Audited is an optional hook: it returns the audit entries of the
	// namespace, oldest first. A case that needs it skips when it is nil.
	Audited func(t *testing.T, ns string) []string
	// StoreRaw is an optional hook: it writes the rule into the store with
	// no Validate, as a row of another version or a restore would. A case
	// that needs it skips when it is nil.
	StoreRaw func(t *testing.T, r outputshare.Rule)
}

// Factory returns a fresh harness for one subtest.
type Factory func(t *testing.T) Harness

// suiteCase is one behavior of the ports. A nil test is a case that waits for
// the first adapter.
type suiteCase struct {
	test func(t *testing.T, h Harness)
	name string
}

// Run runs every case of the suite.
func Run(t *testing.T, newHarness Factory) {
	t.Helper()

	for _, tc := range allCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if tc.test == nil {
				t.Skip(Pending)
			}

			tc.test(t, newHarness(t))
		})
	}
}

// Names returns the names of the cases of the suite, in the order of Run.
// Each call returns a new slice.
func Names() []string {
	cases := allCases()
	names := make([]string, 0, len(cases))

	for _, tc := range cases {
		names = append(names, tc.name)
	}

	return names
}

// allCases returns the cases of the suite. Each call returns a new slice.
func allCases() []suiteCase {
	return slices.Concat(storeCases(), newUnitCases(), checkerCases())
}

// storeCases are the behaviors of Store.
func storeCases() []suiteCase {
	return []suiteCase{
		{nil, "store: put and get round trip a rule on a unit, a collection and a namespace"},
		{nil, "store: put refuses a rule that fails Validate and changes nothing"},
		{nil, "store: put refuses a rule that names a principal"},
		{nil, "store: put refuses a rule that names a whole other namespace"},
		{nil, "store: put keeps a rule that names a unit or a collection of another namespace"},
		{nil, "store: put refuses a second rule with the same producer and consumer"},
		{nil, "store: put refuses the rule past the limit of the producer"},
		{nil, "store: put refuses a stale fence and a fence of another namespace"},
		{nil, "store: a rule across namespaces carries the fence of the producer only"},
		{nil, "store: get and delete of an unknown rule"},
		{nil, "store: get and delete refuse an invalid ID and an invalid namespace"},
		{nil, "store: delete refuses a stale fence"},
		{nil, "store: list returns the rules of one producer ordered by ID"},
		{nil, "store: a rule is invisible to another namespace"},
		{nil, "store: a rename of the producer or of a consumer in the namespace keeps the rule"},
		{nil, "store: a collection made again with the old name does not get the rules"},
		{nil, "store: the purge of a unit removes the rules that name it in its namespace"},
		{nil, "store: find matches a rule on the unit, its collection and its namespace"},
		{nil, "store: find matches a consumer unit, its collection and the namespace of the producer"},
		{nil, "store: find never matches a namespace rule for a consumer of another namespace"},
		{nil, "store: find matches a consumer of another namespace by its reference"},
		{nil, "store: find after a move of the consumer to another collection"},
		{nil, "store: find returns ErrNoRule when no rule matches"},
		{nil, "store: find never matches a stored row that fails Validate"},
		{nil, "store: find refuses an invalid placement"},
		{nil, "store: the setting is nobody for a namespace that never set it"},
		{nil, "store: set setting round trips and refuses an invalid value"},
		{nil, "store: a stored setting that fails Validate gives ErrInvalid"},
		{nil, "store: set setting changes no rule of a unit that exists"},
		{nil, "store: each write appends one audit entry in its transaction"},
	}
}

// newUnitCases are the behaviors of NewUnitRules.
func newUnitCases() []suiteCase {
	return []suiteCase{
		{nil, "new unit: nobody gives no rule"},
		{nil, "new unit: namespace gives one rule that names the namespace of the unit"},
		{nil, "new unit: a unit with no stable ID is refused"},
	}
}

// checkerCases are the behaviors of Checker.
func checkerCases() []suiteCase {
	return []suiteCase{
		{nil, "checker: a principal with unit:read-outputs reads the plain outputs"},
		{nil, "checker: a principal without unit:read-outputs is denied by the grant check"},
		{nil, "checker: a principal with unit:read but no unit:read-outputs is denied"},
		{nil, "checker: a secret output needs unit:read-secrets"},
		{nil, "checker: a rule gives a principal nothing"},
		{nil, "checker: a job with a rule and a starter with the grant reads"},
		{nil, "checker: a job with no rule is denied by the rule check"},
		{nil, "checker: a job with a rule and a starter without the grant is denied by the grant check"},
		{nil, "checker: the job reads the intersection of the rule and the grants of the starter"},
		{nil, "checker: a grant or a rule that changes after the start applies at the next read"},
		{nil, "checker: a starter that is deleted or disabled denies"},
		{nil, "checker: a job of a trigger uses the grants of the last saver of the trigger"},
		{nil, "checker: a job that ended denies"},
		{nil, "checker: a job of another namespace reads only with a rule that names its unit or collection"},
		{nil, "checker: a job of another namespace never matches a namespace rule"},
		{nil, "checker: a stored rule that fails Validate denies"},
		{nil, "checker: an error of a port denies"},
		{nil, "checker: each denial writes an audit entry with the check, the producer and the unit of the job"},
		{nil, "checker: an invalid producer placement is refused"},
	}
}

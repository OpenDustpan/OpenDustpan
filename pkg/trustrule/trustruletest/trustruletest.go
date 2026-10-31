// Package trustruletest is the conformance suite of the Store port of package
// trustrule. A new adapter runs Run with a Factory.
//
// Each case states one rule of the port.
package trustruletest

import (
	"slices"
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/trustrule"
)

// Harness is one fresh, empty store for one subtest.
//
// Namespace makes the namespace called name when it does not exist, and
// returns its current fence. Audit returns the audit entries of a namespace,
// oldest first. Principal makes a principal of the given kind and returns its
// ID: a principal of the namespace ns, or of the installation when ns is
// empty. The namespace exists already. DeletePrincipal deletes the principal
// with the ID id.
type Harness struct {
	Store           trustrule.Store
	Namespace       func(t *testing.T, name string) namespace.Fence
	Audit           func(t *testing.T, ns string) []audit.Entry
	Principal       func(t *testing.T, ns namespace.Name, kind principal.Kind) string
	DeletePrincipal func(t *testing.T, id string)
}

// Factory returns a fresh harness for one subtest.
type Factory func(t *testing.T) Harness

type suiteCase struct {
	name string
	test func(t *testing.T, h Harness)
}

// CaseNames returns the name of each case, in order.
func CaseNames() []string {
	all := cases()
	names := make([]string, 0, len(all))

	for _, tc := range all {
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

			tc.test(t, newHarness(t))
		})
	}
}

// cases lists the rules of the Store port. Each name states one rule.
func cases() []suiteCase {
	return slices.Concat(
		// Create stores the rule, and Get and List give it back as it was
		// stored, in their order, and only in its namespace.
		readCases(),
		// Create refuses a rule that a namespace cannot hold, and changes
		// nothing.
		createCases(),
		// A write carries the fence and the audit entry of the change, and a
		// failure changes nothing.
		writeCases(),
	)
}

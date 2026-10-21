// Package connectiontest is the conformance suite of the Store port of
// package forge: the connections of a namespace, the registration of the
// installation-level GitHub App and the installation-to-namespace map. A new
// adapter runs Run with a Factory.
//
// Each case states one rule of the port.
package connectiontest

import (
	"slices"
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/forge"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Harness is one fresh, empty store for one subtest. Namespace makes the
// namespace called name and returns its current fence. Audit returns the audit
// entries of a scope, oldest first. The scope is a namespace or
// audit.ServerScope.
type Harness struct {
	Store     forge.Store
	Namespace func(t *testing.T, name string) namespace.Fence
	Audit     func(t *testing.T, scope namespace.Name) []audit.Entry
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

func cases() []suiteCase {
	return slices.Concat(
		// A connection belongs to a namespace, and a write carries the fence and
		// the audit entry of the change.
		connectionCases(),
		// The default flag, and the rules for its delete.
		defaultCases(),
		// The registration of the installation-level app.
		registrationCases(),
		// The installation-to-namespace map.
		assignmentCases(),
	)
}

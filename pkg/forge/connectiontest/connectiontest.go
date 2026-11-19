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
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/forge"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Harness is one fresh, empty store for one subtest. Namespace makes the
// namespace called name and returns its current fence. Audit returns the audit
// entries of a scope, oldest first. The scope is a namespace or
// audit.ServerScope. SeedReceipts writes n event receipts of the GitHub kind
// that carry receivedAt, with delivery IDs and body digests that no other
// case uses, in one write that skips the checks of RecordEventReceipt. The
// bound cases need it, because n is the bound of the table. BreakAuditAppends
// makes every later insert of an audit entry fail inside its transaction, as
// only SQL can; the cases that prove a change and its entry commit together
// need it, and skip without it.
type Harness struct {
	Store             forge.Store
	Namespace         func(t *testing.T, name string) namespace.Fence
	Audit             func(t *testing.T, scope namespace.Name) []audit.Entry
	SeedReceipts      func(t *testing.T, n int, receivedAt time.Time)
	BreakAuditAppends func(t *testing.T)
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
		// The old event secret of the registration and its confirmation.
		rotationCases(),
		// The event receipts: the hint and the replay rule.
		receiptCases(),
	)
}

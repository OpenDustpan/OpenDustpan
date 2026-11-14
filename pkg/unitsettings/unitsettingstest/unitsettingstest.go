// Package unitsettingstest is the conformance suite of the Store port of
// package unitsettings. A new adapter runs Run with a Factory.
//
// Each case states one rule of the port.
package unitsettingstest

import (
	"context"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/idempotency"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/unitsettings"
)

// Harness is one fresh, empty store for one subtest. Namespace makes the
// namespace called name and returns its current fence. Unit makes the unit of
// the tool called name in the namespace ns, and returns its reference with the
// stable unit ID set. Audit returns the audit entries of a namespace, oldest
// first.
type Harness struct {
	Store     unitsettings.Store
	Namespace func(t *testing.T, name string) namespace.Fence
	Unit      func(t *testing.T, ns, tool, name string) identity.Object
	Audit     func(t *testing.T, ns string) []audit.Entry
	// Records is the store of replay records that completes in the
	// transaction of a write.
	Records idempotency.Store
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
	out := make([]suiteCase, 0, caseCount)
	out = append(out, recordCases()...)
	out = append(out, writeCases()...)
	out = append(out, deleteCases()...)
	out = append(out, defaultsCases()...)
	out = append(out, entryCases()...)
	out = append(out, idempotencyCases()...)

	return out
}

const (
	// wrongVersion is a version that no record in a case has.
	wrongVersion = 7
	// stepTimeout is the timeout of the step of a case.
	stepTimeout = 90 * time.Second
	// auditPair is the number of audit entries after two writes.
	auditPair  = 2
	refMain    = "main"
	refRelease = "release"
	// writeMs is the time of the audit entries of the cases, in Unix
	// milliseconds.
	writeMs = 1_700_000_000_000
	// caseCount is the number of cases of the suite, for the capacity of the
	// list.
	caseCount = 48
)

// writeTime returns the time of the audit entries of the cases.
func writeTime() time.Time {
	return time.UnixMilli(writeMs).UTC()
}

// poolDefaults returns the unit defaults of ns that name pool.
func poolDefaults(ns namespace.Name, pool string) unitsettings.NamespaceDefaults {
	return unitsettings.NamespaceDefaults{Namespace: ns, Pool: pool, Version: 0, UpdatedAt: time.Time{}}
}

// auditEntry returns a valid audit entry for a write in ns.
func auditEntry(ns namespace.Name) audit.Entry {
	return audit.Entry{
		At: writeTime(), Namespace: ns, Subject: "alice", IDPSubject: "", TokenID: "", Action: "x",
		Object: ns.String(), Version: "", ValuePath: "", ValueCount: nil, Changes: nil, SourceIP: "192.0.2.1",
		UserAgent: "", Channel: audit.ChannelAPI, Kind: audit.KindChange, Outcome: audit.OutcomeSuccess,
		RequestID: "", ObjectID: "", PrevHash: "", Hash: "", Sequence: 0, FenceVersion: 0, PrincipalKind: "",
		JobID: "", Reason: "",
	}
}

// staleFence returns a fence that is older than f.
func staleFence(f namespace.Fence) namespace.Fence {
	f.FailoverVersion--

	return f
}

// settingsFor returns valid settings of unit with every group set.
func settingsFor(unit identity.Object) unitsettings.Settings {
	return unitsettings.Settings{
		Unit:      unit,
		Executor:  "write-back",
		Pool:      "",
		Version:   0,
		UpdatedAt: time.Time{},
		Steps:     nil,
		CodeLocation: unitsettings.CodeLocation{
			Connection: "",
			Repository: "acme/infra",
			Ref:        refMain,
			Directory:  "stacks/prod",
		},
		WriteBack:      unitsettings.WriteBack{Delivery: "direct-push", Files: []string{"Pulumi.prod.yaml"}},
		ReviewRequests: unitsettings.ReviewRequests{Enabled: true},
	}
}

// tryPut writes s and returns the error of the store.
func tryPut(t *testing.T, h Harness, s *unitsettings.Settings, expected int64, fence namespace.Fence) error {
	t.Helper()

	_, err := h.Store.Put(t.Context(), s, expected, fence, auditEntry(fence.Namespace))

	return err
}

// tryDelete removes the record of unit and returns the error of the store.
func tryDelete(t *testing.T, h Harness, unit identity.Object, expected int64, fence namespace.Fence) error {
	t.Helper()

	return h.Store.Delete(t.Context(), unit, expected, fence, auditEntry(fence.Namespace))
}

// trySetDefaults writes d and returns the result and the error of the store.
func trySetDefaults(
	t *testing.T, h Harness, d unitsettings.NamespaceDefaults, expected int64, fence namespace.Fence,
) (unitsettings.NamespaceDefaults, error) {
	t.Helper()

	return h.Store.SetDefaults(t.Context(), d, expected, fence, auditEntry(fence.Namespace))
}

// putIn writes s with the context ctx, which can carry a Completion.
func putIn(
	ctx context.Context, h Harness, s *unitsettings.Settings, expected int64, fence namespace.Fence,
) error {
	_, err := h.Store.Put(ctx, s, expected, fence, auditEntry(fence.Namespace))

	return err
}

// deleteIn removes the record of unit with the context ctx.
func deleteIn(
	ctx context.Context, h Harness, unit identity.Object, expected int64, fence namespace.Fence,
) error {
	return h.Store.Delete(ctx, unit, expected, fence, auditEntry(fence.Namespace))
}

// setDefaultsIn writes d with the context ctx.
func setDefaultsIn(
	ctx context.Context, h Harness, d unitsettings.NamespaceDefaults, expected int64,
	fence namespace.Fence,
) (unitsettings.NamespaceDefaults, error) {
	return h.Store.SetDefaults(ctx, d, expected, fence, auditEntry(fence.Namespace))
}

package sqlite_test

import (
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/unmanaged/unmanagedtest"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/runner"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// unmanagedDomain makes the namespaces and the runner pools that the sweep
// cases need.
type unmanagedDomain struct {
	s *sqlite.Store
}

// Namespace implements unmanagedtest.Harness.Namespace: it makes the namespace
// name with a pool of sweeps and a pool of jobs, and returns its fence.
func (d unmanagedDomain) Namespace(t *testing.T, name string) namespace.Fence {
	t.Helper()

	fence := ensureNamespace(t, d.s, namespace.Name(name))

	ensurePool(t, d.s, fence, unmanagedtest.Pool, runner.PurposeSweeps)
	ensurePool(t, d.s, fence, unmanagedtest.JobPool, runner.PurposeJobs)

	return fence
}

// ensurePool makes the runner pool name in the namespace of fence when it is
// not there.
func ensurePool(t *testing.T, s *sqlite.Store, fence namespace.Fence, name string, purpose runner.Purpose) {
	t.Helper()

	ctx := t.Context()

	pools, err := s.Runners().Pools(ctx, fence.Namespace)
	if err != nil {
		t.Fatalf("list runner pools of %s: %v", fence.Namespace, err)
	}

	for i := range pools {
		if pools[i].Name == name {
			return
		}
	}

	at := time.UnixMilli(1_700_000_000_000).UTC()

	_, err = s.Runners().CreatePool(ctx, runner.Pool{
		ID: "", Namespace: fence.Namespace, Name: name, Purpose: purpose, Lease: runner.DefaultLease,
		Concurrency: 2, Isolation: []runner.Isolation{runner.IsolationKubernetes},
		GitCredentials: runner.GitCredentials{Mode: runner.GitCredentialsForge, Helper: ""},
		CreatedAt:      at, UpdatedAt: at,
	}, fence, []audit.Entry{poolEntry(fence.Namespace, at)})
	if err != nil {
		t.Fatalf("create runner pool %s: %v", name, err)
	}
}

// poolEntry returns the audit entry of a runner pool that a test makes.
func poolEntry(ns namespace.Name, at time.Time) audit.Entry {
	return audit.Entry{
		At:            at,
		Namespace:     ns,
		Subject:       "user:ada",
		IDPSubject:    "",
		TokenID:       "",
		Action:        "namespace:manage-runners",
		Object:        "namespace:" + ns.String(),
		Version:       "",
		ValuePath:     "",
		ValueCount:    nil,
		Changes:       nil,
		SourceIP:      "",
		UserAgent:     "",
		Channel:       audit.ChannelAPI,
		Kind:          audit.KindChange,
		Outcome:       audit.OutcomeSuccess,
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

// newUnmanagedHarness returns the sweep store of a fresh store for one case.
// The finding store and the index land later, so the harness leaves them nil.
func newUnmanagedHarness(t *testing.T) unmanagedtest.Harness {
	t.Helper()

	s := openTemplated(t)

	return unmanagedtest.Harness{
		Index:          nil,
		Links:          nil,
		Findings:       nil,
		Scans:          s.UnmanagedScans(),
		Namespace:      unmanagedDomain{s: s}.Namespace,
		PurgeNamespace: nil,
	}
}

func TestUnmanagedScanStoreConformance(t *testing.T) {
	t.Parallel()

	unmanagedtest.RunScanStore(t, newUnmanagedHarness)
}

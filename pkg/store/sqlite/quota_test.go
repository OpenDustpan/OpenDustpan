package sqlite_test

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/quota"
	"opendustpan.org/opendustpan/internal/quota/quotatest"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// quotaDomain is quotatest.Domain over a Store: it makes the namespaces and
// the fences that the counter needs, and that other ports own.
type quotaDomain struct {
	t *testing.T
	s *sqlite.Store
}

// CreateNamespace implements quotatest.Domain.
func (d quotaDomain) CreateNamespace(ctx context.Context, name namespace.Name) (namespace.Fence, error) {
	d.t.Helper()

	ns := namespace.Namespace{
		ID:                  namespace.NewID(),
		CreatedAt:           time.UnixMilli(1_700_000_000_000).UTC(),
		Name:                name,
		PrimaryInstallation: "eu-west",
		FailoverVersion:     1,
		Overrides:           namespace.Overrides{Forced: false, Unlocked: false},
	}
	if err := d.s.CreateNamespace(ctx, ns); err != nil {
		return namespace.Fence{}, fmt.Errorf("create namespace %s: %w", name, err)
	}

	return ns.Fence(), nil
}

// BumpFence implements quotatest.Domain.
func (d quotaDomain) BumpFence(ctx context.Context, fence namespace.Fence) (namespace.Fence, error) {
	d.t.Helper()

	if _, err := d.s.BumpFailoverVersions(ctx); err != nil {
		return namespace.Fence{}, fmt.Errorf("bump failover versions: %w", err)
	}

	ns, err := d.s.GetNamespace(ctx, fence.Namespace)
	if err != nil {
		return namespace.Fence{}, fmt.Errorf("get namespace %s: %w", fence.Namespace, err)
	}

	return ns.Fence(), nil
}

// newQuotaHarness returns one fresh store for one case of the suite.
func newQuotaHarness(t *testing.T) quotatest.Harness {
	t.Helper()

	path := filepath.Join(t.TempDir(), "dustpan.db")

	copyTemplate(t, path)

	s := openPath(t, path)

	return quotatest.Harness{Counter: s.QuotaCounter(), Domain: quotaDomain{t: t, s: s}}
}

func TestQuotaCounterConformance(t *testing.T) {
	t.Parallel()

	quotatest.Run(t, newQuotaHarness)
}

// quotaTime is the time of the adapter cases.
func quotaTime() time.Time {
	return time.Unix(1_790_000_000, 0).UTC()
}

// quotaFence creates the namespace name in s and returns its fence.
func quotaFence(t *testing.T, s *sqlite.Store, name namespace.Name) namespace.Fence {
	t.Helper()

	fence, err := (quotaDomain{t: t, s: s}).CreateNamespace(t.Context(), name)
	if err != nil {
		t.Fatalf("create namespace %s: %v", name, err)
	}

	return fence
}

// quotaUsages returns the usage of each check, and fails the test when the
// read fails.
func quotaUsages(t *testing.T, s *sqlite.Store, checks []quota.Check, at time.Time) []quota.Usage {
	t.Helper()

	got, err := s.QuotaCounter().Usage(t.Context(), checks, at)
	if err != nil {
		t.Fatalf("Usage() = %v", err)
	}

	if len(got) != len(checks) {
		t.Fatalf("Usage() gave %d usages for %d checks", len(got), len(checks))
	}

	return got
}

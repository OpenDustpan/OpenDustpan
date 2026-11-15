package postgres_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/quota/quotatest"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/postgres"
)

// quotaDomain is quotatest.Domain over a Store: it makes the namespaces and
// the fences that the counter needs, and that other ports own.
type quotaDomain struct {
	t *testing.T
	s *postgres.Store
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

func TestQuotaCounterConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	quotatest.Run(t, func(t *testing.T) quotatest.Harness {
		t.Helper()

		s := open(t, base)

		return quotatest.Harness{Counter: s.QuotaCounter(), Domain: quotaDomain{t: t, s: s}}
	})
}

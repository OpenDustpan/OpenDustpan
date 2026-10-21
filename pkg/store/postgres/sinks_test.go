package postgres_test

import (
	"context"
	"fmt"
	"os"
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/notify/notifytest"
	"opendustpan.org/opendustpan/pkg/store/postgres"
)

// sinkDomain makes the namespaces that sinks need and reads the audit entries
// that the store appends.
type sinkDomain struct {
	t *testing.T
	s *postgres.Store
}

var _ notifytest.Domain = sinkDomain{t: nil, s: nil}

func (d sinkDomain) CreateNamespace(_ context.Context, name namespace.Name) (namespace.Fence, error) {
	return ensureNamespace(d.t, d.s, name), nil
}

func (d sinkDomain) BumpFence(ctx context.Context, fence namespace.Fence) (namespace.Fence, error) {
	if _, err := d.s.BumpFailoverVersions(ctx); err != nil {
		return namespace.Fence{}, fmt.Errorf("bump failover versions: %w", err)
	}

	ns, err := d.s.GetNamespace(ctx, fence.Namespace)
	if err != nil {
		return namespace.Fence{}, fmt.Errorf("get namespace: %w", err)
	}

	return ns.Fence(), nil
}

func (d sinkDomain) AuditEntries(_ context.Context, scope namespace.Name) ([]audit.Entry, error) {
	return allAuditEntries(d.t, d.s, scope), nil
}

func TestSinkStoreConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	notifytest.RunSinks(t, func(t *testing.T) notifytest.SinkHarness {
		t.Helper()

		s := open(t, base)

		return notifytest.SinkHarness{Sinks: s.NotificationSinks(), Domain: sinkDomain{t: t, s: s}}
	})
}

package postgres_test

import (
	"context"
	"fmt"
	"os"
	"slices"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/notify/notifytest"
	"opendustpan.org/opendustpan/pkg/store/postgres"
)

// sinkDomain makes the namespaces that sinks need and reads the audit entries
// that the store appends.
type sinkDomain struct {
	s *postgres.Store
}

var _ notifytest.Domain = sinkDomain{s: nil}

func (d sinkDomain) CreateNamespace(ctx context.Context, name namespace.Name) (namespace.Fence, error) {
	if existing, err := d.s.GetNamespace(ctx, name); err == nil {
		return existing.Fence(), nil
	}

	ns := namespace.Namespace{
		ID: namespace.NewID(), CreatedAt: time.UnixMilli(1_700_000_000_000).UTC(), Name: name, HomeRegion: "eu-west",
		FailoverVersion: 1, Overrides: namespace.Overrides{Forced: false, Unlocked: false},
	}
	if err := d.s.CreateNamespace(ctx, ns); err != nil {
		return namespace.Fence{}, fmt.Errorf("create namespace %s: %w", name, err)
	}

	return ns.Fence(), nil
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

func (d sinkDomain) AuditEntries(ctx context.Context, scope namespace.Name) ([]audit.Entry, error) {
	var out []audit.Entry

	for page := 1; ; page++ {
		listing, err := d.s.List(ctx, scope, audit.Page{Number: page, Size: audit.MaxPageSize})
		if err != nil {
			return nil, fmt.Errorf("list audit of %s: %w", scope, err)
		}

		out = append(out, listing.Entries...)

		if len(listing.Entries) < audit.MaxPageSize {
			break
		}
	}

	slices.SortFunc(out, func(a, b audit.Entry) int { return int(a.Sequence - b.Sequence) })

	return out, nil
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

		return notifytest.SinkHarness{Sinks: s.NotificationSinks(), Domain: sinkDomain{s: s}}
	})
}

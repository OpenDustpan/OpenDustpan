package postgres_test

import (
	"context"
	"fmt"
	"os"
	"slices"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/selfservice/selfservicetest"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/postgres"
)

// blueprintDomain is selfservicetest.BlueprintDomain over a Store.
type blueprintDomain struct {
	t *testing.T
	s *postgres.Store
}

func (d blueprintDomain) CreateNamespace(ctx context.Context, name namespace.Name) (namespace.Fence, error) {
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

func (d blueprintDomain) BumpFence(ctx context.Context, fence namespace.Fence) (namespace.Fence, error) {
	d.t.Helper()

	if _, err := d.s.BumpFailoverVersions(ctx); err != nil {
		return namespace.Fence{}, fmt.Errorf("bump failover versions: %w", err)
	}

	ns, err := d.s.GetNamespace(ctx, fence.Namespace)
	if err != nil {
		return namespace.Fence{}, fmt.Errorf("get namespace: %w", err)
	}

	return ns.Fence(), nil
}

func (d blueprintDomain) AuditEntries(ctx context.Context, ns namespace.Name) ([]audit.Entry, error) {
	d.t.Helper()

	var out []audit.Entry

	for page := 1; ; page++ {
		listing, err := d.s.List(ctx, ns, audit.Page{Number: page, Size: audit.MaxPageSize})
		if err != nil {
			return nil, fmt.Errorf("list audit of %s: %w", ns, err)
		}

		out = append(out, listing.Entries...)

		if len(listing.Entries) < audit.MaxPageSize {
			break
		}
	}

	slices.SortFunc(out, func(a, b audit.Entry) int { return int(a.Sequence - b.Sequence) })

	return out, nil
}

func selfServiceBlueprintHarness(base string) selfservicetest.BlueprintFactory {
	return func(t *testing.T) selfservicetest.BlueprintHarness {
		t.Helper()

		s := openURL(t, isolatedURL(t, base))

		return selfservicetest.BlueprintHarness{Blueprints: s.SelfServiceBlueprints(), Domain: blueprintDomain{t: t, s: s}}
	}
}

func TestSelfServiceBlueprintConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	selfservicetest.RunBlueprints(t, selfServiceBlueprintHarness(base))
}

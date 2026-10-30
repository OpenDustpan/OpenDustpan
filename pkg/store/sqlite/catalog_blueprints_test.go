package sqlite_test

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/catalog/catalogtest"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// blueprintDomain is catalogtest.BlueprintDomain over a Store.
type blueprintDomain struct {
	t *testing.T
	s *sqlite.Store
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

func newBlueprintHarness(t *testing.T) catalogtest.BlueprintHarness {
	t.Helper()

	path := filepath.Join(t.TempDir(), "dustpan.db")

	copyTemplate(t, path)

	s := openPath(t, path)

	return catalogtest.BlueprintHarness{Blueprints: s.CatalogBlueprints(), Domain: blueprintDomain{t: t, s: s}}
}

func TestCatalogBlueprintConformance(t *testing.T) {
	t.Parallel()

	catalogtest.RunBlueprints(t, newBlueprintHarness)
}

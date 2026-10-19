package postgres_test

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/history"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/postgres"
	"opendustpan.org/opendustpan/pkg/store/storetest"
	"opendustpan.org/opendustpan/pkg/unitimport"
	"opendustpan.org/opendustpan/pkg/unitimport/unitimporttest"
)

// importHarness returns the conformance harness over the store of imports of s.
func importHarness(s *postgres.Store) unitimporttest.Harness {
	imports := s.Imports()

	return unitimporttest.Harness{
		Store: imports,
		Units: imports,
		Namespace: func(t *testing.T, name string) namespace.Fence {
			t.Helper()

			return contextNamespace(t, s, name)
		},
		SeedLedger: imports.SeedLedgerRow,
		SetTarget: func(ctx context.Context, _ namespace.Name, key unitimport.UnitKey, target history.Unit) error {
			return imports.SeedTarget(ctx, key, target)
		},
		Audited: func(t *testing.T, ns namespace.Name) []audit.Entry {
			t.Helper()

			return importAudited(t, s, ns)
		},
	}
}

// importAudited returns the audit entries of ns with an action that starts
// with "import:", oldest first.
func importAudited(t *testing.T, s *postgres.Store, ns namespace.Name) []audit.Entry {
	t.Helper()

	var out []audit.Entry

	for page := 1; ; page++ {
		listing, err := s.List(t.Context(), ns, audit.Page{Number: page, Size: audit.MaxPageSize})
		if err != nil {
			t.Fatalf("list audit: %v", err)
		}

		for i := range listing.Entries {
			if strings.HasPrefix(listing.Entries[i].Action, "import:") {
				out = append(out, listing.Entries[i])
			}
		}

		if len(listing.Entries) < audit.MaxPageSize {
			break
		}
	}

	slices.SortFunc(out, func(a, b audit.Entry) int { return int(a.Sequence - b.Sequence) })

	return out
}

func TestImportsConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	unitimporttest.RunStore(t, func(t *testing.T) unitimporttest.Harness {
		t.Helper()

		return importHarness(open(t, base))
	})
}

// ImportPorts implements storetest.ImportSeeder.
func (s *backupStore) ImportPorts() storetest.ImportPorts {
	imports := s.Imports()

	return storetest.ImportPorts{
		Store:      imports,
		Units:      imports,
		SeedLedger: imports.SeedLedgerRow,
		SetTarget: func(ctx context.Context, _ namespace.Name, key unitimport.UnitKey, target history.Unit) error {
			return imports.SeedTarget(ctx, key, target)
		},
	}
}

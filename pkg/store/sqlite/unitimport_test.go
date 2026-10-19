package sqlite_test

import (
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/history"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/unitimport"
	"opendustpan.org/opendustpan/pkg/unitimport/unitimporttest"
)

// importHarness returns the conformance harness over the store of imports of s.
func importHarness(s *sqlite.Store) unitimporttest.Harness {
	imports := s.Imports()

	return unitimporttest.Harness{
		Store: imports,
		Units: imports,
		Namespace: func(t *testing.T, name string) namespace.Fence {
			t.Helper()

			return contextNamespace(t, s, name)
		},
		SeedLedger: func(t *testing.T, ns namespace.Name, row unitimport.LedgerRow) {
			t.Helper()

			if err := imports.SeedLedgerRow(t.Context(), ns, row); err != nil {
				t.Fatalf("seed ledger row: %v", err)
			}
		},
		SetTarget: func(t *testing.T, _ namespace.Name, key unitimport.UnitKey, target history.Unit) {
			t.Helper()

			if err := imports.SeedTarget(t.Context(), key, target); err != nil {
				t.Fatalf("seed target: %v", err)
			}
		},
		Audited: func(t *testing.T, ns namespace.Name) []audit.Entry {
			t.Helper()

			return importAudited(t, s, ns)
		},
	}
}

// importAudited returns the audit entries of ns with an action that starts
// with "import:", oldest first.
func importAudited(t *testing.T, s *sqlite.Store, ns namespace.Name) []audit.Entry {
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

	unitimporttest.RunStore(t, func(t *testing.T) unitimporttest.Harness {
		t.Helper()

		return importHarness(open(t))
	})
}

package sqlite_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/unitimport"
	"opendustpan.org/opendustpan/internal/unitimport/unitimporttest"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/history"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/profile"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/store/storetest"
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

// IssuerPorts implements storetest.IssuerSeeder.
func (s *backupStore) IssuerPorts() storetest.IssuerPorts {
	return storetest.IssuerPorts{Store: s.Issuer()}
}

// ProfileStore implements storetest.ProfileSeeder.
//
//nolint:ireturn // The method implements storetest.ProfileSeeder, which returns the port.
func (s *backupStore) ProfileStore() profile.Store {
	return s.Profiles()
}

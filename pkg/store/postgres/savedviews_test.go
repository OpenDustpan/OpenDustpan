package postgres_test

import (
	"os"
	"slices"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/savedview/savedviewtest"
	"opendustpan.org/opendustpan/pkg/store/postgres"
)

// savedViewHarness returns a harness over a fresh store. The namespaces it
// makes are homed with failover version 1.
func savedViewHarness(base string) savedviewtest.Factory {
	return func(t *testing.T) savedviewtest.Harness {
		t.Helper()

		return newSavedViewHarness(t, open(t, base))
	}
}

func newSavedViewHarness(t *testing.T, s *postgres.Store) savedviewtest.Harness {
	t.Helper()

	return savedviewtest.Harness{
		Store: s.SavedViews(),
		Namespace: func(t *testing.T, name string) namespace.Fence {
			t.Helper()

			return ensureNamespace(t, s, namespace.Name(name))
		},
		Audit: func(t *testing.T, ns string) []audit.Entry {
			t.Helper()

			return allAuditEntries(t, s, namespace.Name(ns))
		},
	}
}

// ensureNamespace makes the namespace name when it does not exist, and returns
// its current fence.
func ensureNamespace(t *testing.T, s *postgres.Store, name namespace.Name) namespace.Fence {
	t.Helper()

	if existing, err := s.GetNamespace(t.Context(), name); err == nil {
		return existing.Fence()
	}

	ns := namespace.Namespace{
		ID: namespace.NewID(), CreatedAt: time.UnixMilli(1_700_000_000_000).UTC(), Name: name, HomeRegion: "eu-west",
		FailoverVersion: 1, Overrides: namespace.Overrides{Forced: false, Unlocked: false},
	}
	if err := s.CreateNamespace(t.Context(), ns); err != nil {
		t.Fatalf("create namespace %s: %v", name, err)
	}

	return ns.Fence()
}

// allAuditEntries returns every audit entry of ns, oldest first.
func allAuditEntries(t *testing.T, s *postgres.Store, ns namespace.Name) []audit.Entry {
	t.Helper()

	var out []audit.Entry

	for page := 1; ; page++ {
		listing, err := s.List(t.Context(), ns, audit.Page{Number: page, Size: audit.MaxPageSize})
		if err != nil {
			t.Fatalf("list audit of %s: %v", ns, err)
		}

		out = append(out, listing.Entries...)

		if len(listing.Entries) < audit.MaxPageSize {
			break
		}
	}

	slices.SortFunc(out, func(a, b audit.Entry) int { return int(a.Sequence - b.Sequence) })

	return out
}

func TestSavedViewConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	savedviewtest.Run(t, savedViewHarness(base))
}

package sqlite_test

import (
	"path/filepath"
	"slices"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/savedview"
	"opendustpan.org/opendustpan/pkg/savedview/savedviewtest"
	"opendustpan.org/opendustpan/pkg/store/internal/viewstore"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// savedViewHarness returns a harness over a fresh store. The namespaces it
// makes are homed with failover version 1.
func savedViewHarness(t *testing.T) savedviewtest.Harness {
	t.Helper()

	path := filepath.Join(t.TempDir(), "dustpan.db")

	copyTemplate(t, path)

	s := openPath(t, path)

	return savedviewtest.Harness{
		Seed: func(t *testing.T, views []savedview.View) {
			t.Helper()

			seedSavedViews(t, path, views)
		},
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

// seedSavedViews inserts views into the database file at path through a
// second handle, in one transaction, with the query encoding of the store.
func seedSavedViews(t *testing.T, path string, views []savedview.View) {
	t.Helper()

	raw, err := openRawDB(path)
	if err != nil {
		t.Fatalf("seed saved views: %v", err)
	}

	defer func() {
		if closeErr := raw.Close(); closeErr != nil {
			t.Errorf("close the seed handle: %v", closeErr)
		}
	}()

	tx, err := raw.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatalf("seed saved views: begin: %v", err)
	}

	defer func() {
		_ = tx.Rollback() //nolint:errcheck // A rollback after the commit has nothing to undo.
	}()

	for _, v := range views {
		query, encodeErr := viewstore.EncodeQuery(v.Query)
		if encodeErr != nil {
			t.Fatalf("seed saved views: encode the query: %v", encodeErr)
		}

		if _, execErr := tx.ExecContext(t.Context(), `INSERT INTO saved_views
			(id, namespace_id, owner, name, query_json, created_at_ms)
			VALUES (?, (SELECT id FROM namespaces WHERE name = ?), ?, ?, ?, ?)`,
			string(v.ID), v.Namespace.String(), v.Owner, v.Name, query, v.CreatedAt.UnixMilli(),
		); execErr != nil {
			t.Fatalf("seed saved views: insert %q of %s: %v", v.Name, v.Owner, execErr)
		}
	}

	if commitErr := tx.Commit(); commitErr != nil {
		t.Fatalf("seed saved views: commit: %v", commitErr)
	}
}

// ensureNamespace makes the namespace name when it does not exist, and returns
// its current fence.
func ensureNamespace(t *testing.T, s *sqlite.Store, name namespace.Name) namespace.Fence {
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
func allAuditEntries(t *testing.T, s *sqlite.Store, ns namespace.Name) []audit.Entry {
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

	savedviewtest.Run(t, savedViewHarness)
}

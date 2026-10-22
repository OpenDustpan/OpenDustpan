package postgres_test

import (
	"context"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/savedview"
	"opendustpan.org/opendustpan/pkg/savedview/savedviewtest"
	"opendustpan.org/opendustpan/pkg/store/internal/viewstore"
	"opendustpan.org/opendustpan/pkg/store/postgres"
)

// savedViewHarness returns a harness over a fresh store. The namespaces it
// makes are homed with failover version 1.
func savedViewHarness(base string) savedviewtest.Factory {
	return func(t *testing.T) savedviewtest.Harness {
		t.Helper()

		schemaURL := isolatedURL(t, base)

		return newSavedViewHarness(t, openURL(t, schemaURL), schemaURL)
	}
}

func newSavedViewHarness(t *testing.T, s *postgres.Store, schemaURL string) savedviewtest.Harness {
	t.Helper()

	return savedviewtest.Harness{
		Seed: func(t *testing.T, views []savedview.View) {
			t.Helper()

			seedSavedViews(t, schemaURL, views)
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

// seedSavedViews inserts views into the schema of schemaURL through a second
// connection, in one transaction, with the query encoding of the store.
func seedSavedViews(t *testing.T, schemaURL string, views []savedview.View) {
	t.Helper()

	conn, err := pgx.Connect(t.Context(), schemaURL)
	if err != nil {
		t.Fatalf("seed saved views: connect: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(context.WithoutCancel(t.Context())); closeErr != nil {
			t.Errorf("close the seed connection: %v", closeErr)
		}
	}()

	rows := make([][]any, 0, len(views))
	nsIDs := map[namespace.Name]string{}

	for i := range views {
		v := &views[i]

		query, encodeErr := viewstore.EncodeQuery(v.Query)
		if encodeErr != nil {
			t.Fatalf("seed saved views: encode the query: %v", encodeErr)
		}

		nsID, ok := nsIDs[v.Namespace]
		if !ok {
			row := conn.QueryRow(t.Context(), "SELECT id FROM namespaces WHERE name = $1", v.Namespace.String())
			if scanErr := row.Scan(&nsID); scanErr != nil {
				t.Fatalf("seed saved views: read namespace %s: %v", v.Namespace, scanErr)
			}

			nsIDs[v.Namespace] = nsID
		}

		rows = append(rows, []any{string(v.ID), nsID, v.Owner, v.Name, query, v.CreatedAt.UnixMilli()})
	}

	columns := []string{"id", "namespace_id", "owner", "name", "query_json", "created_at_ms"}
	_, copyErr := conn.CopyFrom(t.Context(), pgx.Identifier{"saved_views"}, columns, pgx.CopyFromRows(rows))
	if copyErr != nil {
		t.Fatalf("seed saved views: copy: %v", copyErr)
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

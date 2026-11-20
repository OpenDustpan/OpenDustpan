package sqlite_test

import (
	"path/filepath"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/forge/connectiontest"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// gitHostHarness returns a harness over a fresh store. The namespaces it makes
// have failover version 1.
func gitHostHarness(t *testing.T) connectiontest.Harness {
	t.Helper()

	path := filepath.Join(t.TempDir(), "dustpan.db")

	copyTemplate(t, path)

	s := openPath(t, path)

	return connectiontest.Harness{
		Store: s.GitHost(),
		Namespace: func(t *testing.T, name string) namespace.Fence {
			t.Helper()

			return ensureNamespace(t, s, namespace.Name(name))
		},
		Audit: func(t *testing.T, scope namespace.Name) []audit.Entry {
			t.Helper()

			return allAuditEntries(t, s, scope)
		},
		SeedReceipts: func(t *testing.T, n int, receivedAt time.Time) {
			t.Helper()

			seedReceipts(t, path, n, receivedAt)
		},
		BreakAuditAppends: func(t *testing.T) {
			t.Helper()

			if err := breakAuditAppends(t.Context(), path); err != nil {
				t.Fatalf("break audit appends: %v", err)
			}
		},
	}
}

func TestGitHostConformance(t *testing.T) {
	t.Parallel()

	connectiontest.Run(t, gitHostHarness)
}

// seedReceipts writes n receipts of the GitHub kind in one statement on a
// second handle, with no check of the port.
func seedReceipts(t *testing.T, path string, n int, receivedAt time.Time) {
	t.Helper()

	conn, err := openRawDB(path)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(); closeErr != nil {
			t.Errorf("close database: %v", closeErr)
		}
	}()

	const seed = `WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < ?)
INSERT INTO git_host_event_receipts
    (kind, delivery_id, body_digest, installation_id, event_kind, repo, review_id, received_at_ms)
SELECT 'github', 'seed-' || i, lower(printf('%064x', i)), '101', 'review', 'acme/widgets', '7', ? FROM n`

	if _, execErr := conn.ExecContext(t.Context(), seed, n, receivedAt.UnixMilli()); execErr != nil {
		t.Fatalf("seed receipts: %v", execErr)
	}
}

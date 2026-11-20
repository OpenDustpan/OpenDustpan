package postgres_test

import (
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/forge/connectiontest"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/internal/githoststore"
	"opendustpan.org/opendustpan/pkg/store/postgres"
)

// gitHostHarness returns a harness over a fresh store. The namespaces it makes
// have failover version 1.
func gitHostHarness(base string) connectiontest.Factory {
	return func(t *testing.T) connectiontest.Harness {
		t.Helper()

		schemaURL := isolatedURL(t, base)

		return newGitHostHarness(t, openURL(t, schemaURL), schemaURL)
	}
}

func newGitHostHarness(t *testing.T, s *postgres.Store, schemaURL string) connectiontest.Harness {
	t.Helper()

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

			seedReceipts(t, schemaURL, n, receivedAt)
		},
		BreakAuditAppends: func(t *testing.T) {
			t.Helper()

			if err := breakAuditAppends(t.Context(), schemaURL); err != nil {
				t.Fatalf("break audit appends: %v", err)
			}
		},
	}
}

func TestGitHostConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	connectiontest.Run(t, gitHostHarness(base))
}

// seedReceipts writes n receipts of the GitHub kind in one statement on a
// second connection, with no check of the port.
func seedReceipts(t *testing.T, schemaURL string, n int, receivedAt time.Time) {
	t.Helper()

	conn, err := pgx.Connect(t.Context(), schemaURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(t.Context()); closeErr != nil {
			t.Errorf("close: %v", closeErr)
		}
	}()

	const seed = `INSERT INTO git_host_event_receipts
    (kind, delivery_id, body_digest, installation_id, event_kind, repo, review_id, received_at_ms)
SELECT 'github', 'seed-' || $2::bigint || '-' || i, lpad(to_hex($2::bigint), 32, '0') || lpad(to_hex(i), 32, '0'), '101', 'review', 'acme/widgets', '7', $2
FROM generate_series(1, $1::int) AS i`

	if _, execErr := conn.Exec(t.Context(), seed, n, receivedAt.UnixMilli()); execErr != nil {
		t.Fatalf("seed receipts: %v", execErr)
	}

	// The seed stands for rows that RecordEventReceipt wrote, so the counter holds them too.
	const count = `INSERT INTO git_host_event_receipt_count (id, receipts, max_receipts) VALUES (TRUE, $1, $2)
ON CONFLICT (id) DO UPDATE SET receipts = git_host_event_receipt_count.receipts + excluded.receipts`

	if _, execErr := conn.Exec(t.Context(), count, n, githoststore.MaxEventReceipts); execErr != nil {
		t.Fatalf("seed receipt count: %v", execErr)
	}
}

package postgres_test

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/workspace"
	"opendustpan.org/opendustpan/pkg/workspace/workspacetest"
)

// TestClaimStoreConformance runs the claim conformance suite against
// PostgreSQL: the workspace row lock orders the parallel registrations.
func TestClaimStoreConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	workspacetest.RunClaimStore(t, func(t *testing.T) workspacetest.ClaimBackend {
		t.Helper()

		return open(t, base)
	})
}

// TestClaimWaitsForTheWorkspaceRowLock takes the row lock of the workspace in
// a second transaction and shows that WithClaims waits for it, and then runs
// when the lock goes. The row lock is what orders two registrations on one
// workspace on this adapter.
func TestClaimWaitsForTheWorkspaceRowLock(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	schemaURL := isolatedURL(t, base)
	s := openURL(t, schemaURL)
	fence, ws := workspacetest.SeedClaimWorkspace(t, s)

	tx := holdWorkspaceRowLock(t, schemaURL, ws)

	entered := make(chan struct{})
	done := make(chan error, 1)

	go func() {
		done <- s.WithClaims(context.Background(), ws, fence, func(workspace.ClaimTx) error {
			close(entered)

			return nil
		})
	}()

	waitForLockWaiter(t, tx, done)

	select {
	case <-entered:
		t.Fatal("WithClaims ran while another transaction held the workspace row lock")
	case err := <-done:
		t.Fatalf("WithClaims returned %v while another transaction held the workspace row lock", err)
	default:
	}

	if err := tx.Rollback(t.Context()); err != nil {
		t.Fatalf("release the lock: %v", err)
	}

	if err := <-done; err != nil {
		t.Fatalf("WithClaims after the lock went = %v, want nil", err)
	}
}

// waitForLockWaiter asks the database, through tx, until a backend waits for
// a row lock in the statement that locks a workspace for its claims. Each
// round trip is the pause between two asks, so the test reads no clock; the
// test timeout ends a wait that never comes.
func waitForLockWaiter(t *testing.T, tx pgx.Tx, done <-chan error) {
	t.Helper()

	const ask = `SELECT count(*) FROM pg_stat_activity
WHERE wait_event_type = 'Lock' AND query LIKE '%FROM workspaces WHERE id%FOR UPDATE%'`

	for {
		select {
		case err := <-done:
			t.Fatalf("WithClaims returned %v while another transaction held the workspace row lock", err)
		default:
		}

		// The activity view is cached for the life of a transaction: drop the
		// cache, or the first answer stays.
		if _, err := tx.Exec(t.Context(), `SELECT pg_stat_clear_snapshot()`); err != nil {
			t.Fatalf("clear the activity snapshot: %v", err)
		}

		var waiting int

		if err := tx.QueryRow(t.Context(), ask).Scan(&waiting); err != nil {
			t.Fatalf("ask for the lock waiters: %v", err)
		}

		if waiting > 0 {
			return
		}
	}
}

// holdWorkspaceRowLock opens a second connection to the schema, takes the row
// lock of the workspace ws in a transaction and returns the transaction. The
// caller ends it.
func holdWorkspaceRowLock(t *testing.T, schemaURL string, ws workspace.ID) pgx.Tx {
	t.Helper()

	conn, err := pgx.Connect(t.Context(), schemaURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := conn.Close(context.Background()); closeErr != nil {
			t.Errorf("close the connection: %v", closeErr)
		}
	})

	tx, err := conn.Begin(t.Context())
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	if _, err = tx.Exec(t.Context(), `SELECT id FROM workspaces WHERE id = $1 FOR UPDATE`, ws.String()); err != nil {
		t.Fatalf("lock the workspace row: %v", err)
	}

	return tx
}

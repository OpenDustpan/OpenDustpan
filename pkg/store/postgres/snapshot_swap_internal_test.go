package postgres

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// swapPath is one way to apply a staged restore: each runs its own swap.
type swapPath struct {
	name  string
	apply func(ctx context.Context, s *Store) error
}

// swapPaths returns the two swaps of a restore: the plain one and the one
// that keeps the access tables.
func swapPaths() []swapPath {
	return []swapPath{
		{name: "plain restore", apply: func(ctx context.Context, s *Store) error { return s.ApplyStaging(ctx) }},
		{name: "restore that keeps access", apply: func(ctx context.Context, s *Store) error {
			_, err := s.ApplyStagingKeepingAccess(ctx)

			return err
		}},
	}
}

// stageOwnExport opens a store with the namespace of newLockNamespace at
// failover version 1, and stages a portable export of it. It returns the
// store and its live schema. The swap then sets the failover version to 2.
func stageOwnExport(t *testing.T) (*Store, string) {
	t.Helper()

	ctx := t.Context()
	s := openKeepLockStore(t, testPostgresURL(t))

	if err := s.CreateNamespace(ctx, newLockNamespace()); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	sink := &keepLockSink{files: map[string]*bytes.Buffer{}}

	sum, files, err := s.ExportPortable(ctx, sink.open)
	if err != nil {
		t.Fatalf("export portable: %v", err)
	}

	if _, err := s.StagePortable(ctx, sink.read, sum, files, storetest.StageAs(t, s)); err != nil {
		t.Fatalf("stage portable: %v", err)
	}

	return s, liveSchema(ctx, t, s)
}

// errNoSwapWait reports a swap that never waited for a lock.
var errNoSwapWait = errors.New("the swap did not wait for a lock")

// waitForSwap returns when a session waits for a lock in a statement that
// names the live schema live and starts with prefix. Other tests share the
// database, so the schema name selects the swap of this test.
func waitForSwap(ctx context.Context, t *testing.T, pool *pgxpool.Pool, live, prefix string) {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)

	for time.Now().Before(deadline) {
		var waiting int

		query := "SELECT count(*) FROM pg_stat_activity WHERE wait_event_type = 'Lock'" +
			" AND pid <> pg_backend_pid() AND starts_with(query, $1) AND strpos(query, $2) > 0"
		if err := pool.QueryRow(ctx, query, prefix, live).Scan(&waiting); err != nil {
			t.Fatalf("read waiting sessions: %v", err)
		}

		if waiting > 0 {
			return
		}

		select {
		case <-time.After(20 * time.Millisecond):
		case <-ctx.Done():
			t.Fatalf("wait for the swap: %v", ctx.Err())
		}
	}

	t.Fatal(errNoSwapWait)
}

// TestSnapshotReadInDescendingTableOrderDuringSwap proves that a snapshot
// read that holds a table and then reads a table that is older in the
// schema does not deadlock with a restore swap. The drop of the old schema
// locks its tables from the oldest, so without the swap lock the drop holds
// checkpoints and waits for stacks, while the read holds stacks and waits for
// checkpoints. The read must answer, and the swap must end after it.
func TestSnapshotReadInDescendingTableOrderDuringSwap(t *testing.T) {
	t.Parallel()

	for _, path := range swapPaths() {
		t.Run(path.name, func(t *testing.T) {
			t.Parallel()

			checkDescendingReadDuringSwap(t, path)
		})
	}
}

// checkDescendingReadDuringSwap runs the descending read of
// TestSnapshotReadInDescendingTableOrderDuringSwap while path swaps.
func checkDescendingReadDuringSwap(t *testing.T, path swapPath) {
	t.Helper()

	ctx := t.Context()
	s, live := stageOwnExport(t)
	applied := make(chan error, 1)

	readErr := s.inSnapshotTx(ctx, func(q *db.Queries) error {
		// The stacks table is newer than the checkpoints table.
		_, err := q.GetStackID(ctx, db.GetStackIDParams{Namespace: "locked", Project: "web", Name: "prod"})
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}

		go func() { applied <- path.apply(ctx, s) }()

		// Without the swap lock the swap waits at the drop; with it the swap
		// waits at the lock. Both statements name the live schema.
		waitForSwap(ctx, t, s.pool, live, "")

		_, err = q.GetLatestCheckpoint(ctx, "stk-none")
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}

		return err
	})
	if readErr != nil {
		t.Fatalf("read during the swap: %v, want an answer", readErr)
	}

	if err := <-applied; err != nil {
		t.Fatalf("apply the staged restore: %v", err)
	}
}

// TestSnapshotReadThatStartsDuringSwapSeesTheSwap proves that a snapshot
// read that starts while a restore swap runs sees the committed result of
// the swap, with the changes that the swap itself makes: the failover
// version that the swap increments. A holder keeps a live table, so the swap
// stops at the drop of the old schema, after the renames. The read starts
// there. Its first statement reads the control schema, which the drop does
// not lock, so without the swap lock it takes its snapshot before the swap
// commits. The second statement then reads the restored namespaces without
// the increment.
func TestSnapshotReadThatStartsDuringSwapSeesTheSwap(t *testing.T) {
	t.Parallel()

	for _, path := range swapPaths() {
		t.Run(path.name, func(t *testing.T) {
			t.Parallel()

			checkReadThatStartsDuringSwap(t, path)
		})
	}
}

// versionAnswer is the answer of the read of a failover version.
type versionAnswer struct {
	err     error
	version int64
}

// checkReadThatStartsDuringSwap runs the read of
// TestSnapshotReadThatStartsDuringSwapSeesTheSwap while path swaps.
func checkReadThatStartsDuringSwap(t *testing.T, path swapPath) {
	t.Helper()

	ctx := t.Context()
	s, live := stageOwnExport(t)

	holder, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin holder: %v", err)
	}

	defer func() {
		//nolint:errcheck // The holder commits on the main path.
		_ = holder.Rollback(context.WithoutCancel(ctx))
	}()

	lockQuery := "LOCK TABLE " + pgx.Identifier{live, "stacks"}.Sanitize() + " IN ACCESS SHARE MODE"
	if _, lockErr := holder.Exec(ctx, lockQuery); lockErr != nil {
		t.Fatalf("hold a table of the live schema: %v", lockErr)
	}

	applied := make(chan error, 1)

	go func() { applied <- path.apply(ctx, s) }()

	waitForSwap(ctx, t, s.pool, live, "DROP SCHEMA")

	read := make(chan versionAnswer, 1)

	go func() { read <- readFailoverVersion(ctx, s) }()

	// Give the read time to answer before the swap ends. Without the swap
	// lock it answers here; with it, it waits for the swap.
	<-time.After(lockWait)

	if commitErr := holder.Commit(ctx); commitErr != nil {
		t.Fatalf("commit holder: %v", commitErr)
	}

	if applyErr := <-applied; applyErr != nil {
		t.Fatalf("apply the staged restore: %v", applyErr)
	}

	got := <-read
	if got.err != nil || got.version != 2 {
		t.Fatalf("failover version read during the swap = %d, %v, want 2 from the swap", got.version, got.err)
	}
}

// readFailoverVersion reads the installation state and then the failover
// version of the namespace "locked", in one snapshot read.
func readFailoverVersion(ctx context.Context, s *Store) versionAnswer {
	var version int64

	err := s.inSnapshotTx(ctx, func(q *db.Queries) error {
		if _, err := q.GetInstallationState(ctx); err != nil {
			return err
		}

		row, err := q.GetNamespace(ctx, "locked")
		version = row.FailoverVersion

		return err
	})

	return versionAnswer{err: err, version: version}
}

// TestReadConflictReportsUnavailable proves that a read that PostgreSQL ends
// with a deadlock or a serialization failure reports
// namespace.ErrUnavailable with no driver text, and that other errors stay
// as they are.
func TestReadConflictReportsUnavailable(t *testing.T) {
	t.Parallel()

	for _, code := range []string{"40P01", "40001"} {
		driverErr := new(pgconn.PgError)
		driverErr.Code = code
		driverErr.Message = "driver text"

		got := readConflict(fmt.Errorf("select: %w", driverErr))
		if !errors.Is(got, namespace.ErrUnavailable) || strings.Contains(got.Error(), "driver text") {
			t.Errorf("readConflict(%s) = %v, want namespace.ErrUnavailable without driver text", code, got)
		}
	}

	other := new(pgconn.PgError)
	other.Code = "23505"
	other.Message = "driver text"
	if got := readConflict(other); !errors.Is(got, other) || errors.Is(got, namespace.ErrUnavailable) {
		t.Errorf("readConflict(23505) = %v, want the error unchanged", got)
	}
}

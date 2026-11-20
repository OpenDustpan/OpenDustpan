package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/internal/store/postgres/internal/db"
)

// TestAppendAuditEntryRefusesServerScope proves that appendAuditEntry refuses
// an entry of the scope @server and stores nothing. The refusal is a
// defense-in-depth layer: the type serverAuditTx already keeps an append of
// @server out of appendAuditEntry. The test passes plain queries to
// appendAuditEntry, as a new caller could.
//
// Mutation: delete the refusal. The entry then reaches appendEntry, the
// append succeeds, and the test fails.
func TestAppendAuditEntryRefusesServerScope(t *testing.T) {
	t.Parallel()

	s, _ := createLockSetup(t, "acme")

	before, _ := serverScopeRows(t, s)

	tx, err := s.pool.Begin(t.Context())
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	t.Cleanup(func() { rollbackTestTx(t, tx) })

	_, err = appendAuditEntry(t.Context(), db.New(tx), serverScopeEntry("server:append"))
	if !errors.Is(err, audit.ErrInvalidEntry) {
		t.Fatalf("appendAuditEntry with an entry of @server: err = %v, want audit.ErrInvalidEntry", err)
	}

	rollbackTestTx(t, tx)

	after, _ := serverScopeRows(t, s)

	if len(after) != len(before) {
		t.Fatalf("the server audit scope holds %d entries after the refused append, want %d", len(after), len(before))
	}
}

// TestRunServerAuditTxLockErrorStopsFn proves that runServerAuditTx fails
// closed: when the advisory lock of the scope @server fails, it returns the
// error and never runs fn. Nothing reaches the log either.
//
// Mutation: ignore the lock error. Then fn runs and the test fails on the
// error and on the flag.
func TestRunServerAuditTxLockErrorStopsFn(t *testing.T) {
	t.Parallel()

	s, _ := createLockSetup(t, "acme")

	before, _ := serverScopeRows(t, s)

	run := func(_ context.Context, fn func(q *db.Queries) error) error {
		return fn(db.New(lockFailTx{lockErr: errLockRefused, otherErr: errBeforeLock}))
	}

	ran := false

	err := runServerAuditTx(t.Context(), run, func(tx serverAuditTx) error {
		ran = true

		_, appendErr := appendServerEntry(t.Context(), tx, serverScopeEntry("server:append"))

		return appendErr
	})
	if !errors.Is(err, errLockRefused) {
		t.Fatalf("runServerAuditTx with a refused lock: err = %v, want the lock error", err)
	}

	if ran {
		t.Fatal("runServerAuditTx ran fn after the lock failed")
	}

	after, _ := serverScopeRows(t, s)

	if len(after) != len(before) {
		t.Fatalf("the server audit scope holds %d entries after the refused lock, want %d", len(after), len(before))
	}
}

// TestAppendServerEntryRefusesNamespaceScope proves that appendServerEntry
// refuses an entry that names a scope other than @server and stores nothing.
func TestAppendServerEntryRefusesNamespaceScope(t *testing.T) {
	t.Parallel()

	s, ns := createLockSetup(t, "acme")

	tx, err := s.pool.Begin(t.Context())
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	t.Cleanup(func() { rollbackTestTx(t, tx) })

	entry := serverScopeEntry("namespace:defaults")
	entry.Namespace = ns.Name

	_, err = appendServerEntry(t.Context(), serverAuditTx{q: db.New(tx)}, entry)
	if !errors.Is(err, audit.ErrInvalidEntry) {
		t.Fatalf("appendServerEntry with an entry of a namespace: err = %v, want audit.ErrInvalidEntry", err)
	}

	rollbackTestTx(t, tx)

	if got := auditRowsInScope(t, s, ns.Name); got != 0 {
		t.Fatalf("the namespace %q holds %d audit entries after the refused append, want 0", ns.Name, got)
	}
}

// errLockRefused and errBeforeLock are the errors of the transaction of
// TestRunServerAuditTxLockErrorStopsFn: its advisory lock fails with the
// first, and a statement that runs before the lock reads the second.
var (
	errLockRefused = errors.New("the advisory lock was refused")
	errBeforeLock  = errors.New("a statement ran before the lock")
)

// lockFailTx is a db.DBTX whose execution of the advisory lock fails with
// lockErr. Any other statement is a program error of the caller: the lock
// comes first, so a test reads otherErr only when the caller ran a statement
// before the lock.
type lockFailTx struct {
	lockErr  error
	otherErr error
}

// Exec returns lockErr for the advisory lock and otherErr for any other
// statement.
func (d lockFailTx) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	if strings.Contains(sql, "pg_advisory_xact_lock") {
		return pgconn.CommandTag{}, d.lockErr
	}

	return pgconn.CommandTag{}, d.otherErr
}

// Query returns otherErr.
func (d lockFailTx) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, d.otherErr
}

// QueryRow returns a row that scans to otherErr.
func (d lockFailTx) QueryRow(context.Context, string, ...any) pgx.Row {
	return failedRow{err: d.otherErr}
}

// failedRow is a pgx.Row whose scan fails with err.
type failedRow struct {
	err error
}

// Scan returns err.
func (r failedRow) Scan(...any) error {
	return r.err
}

// rollbackTestTx ends the transaction tx and tolerates one that ended
// already.
func rollbackTestTx(t *testing.T, tx pgx.Tx) {
	t.Helper()

	err := tx.Rollback(context.Background())
	if err != nil && !errors.Is(err, pgx.ErrTxClosed) {
		t.Fatalf("roll back: %v", err)
	}
}

// auditRowsInScope returns the count of the audit entries in scope.
func auditRowsInScope(t *testing.T, s *Store, scope namespace.Name) int {
	t.Helper()

	var n int

	err := s.pool.QueryRow(t.Context(),
		"SELECT count(*) FROM audit_entries WHERE namespace = $1", scope.String()).Scan(&n)
	if err != nil {
		t.Fatalf("count the audit entries in %q: %v", scope, err)
	}

	return n
}

package postgres

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"opendustpan.org/opendustpan/internal/store/postgres/internal/db"
	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/principal"
)

// errScriptedStatement is the error of the statement that a scriptTx fails.
var errScriptedStatement = errors.New("scripted statement failure")

// errScriptScan is the error of a scan that the scripted rows cannot serve.
var errScriptScan = errors.New("scripted scan not supported")

// TestBreakGlassScopeCountsFailsClosed proves that the namespace counts of a
// restore swap fail closed. A failed list of the namespaces, or a failed
// count of one namespace, returns ErrBreakGlassUnavailable and no counts, so
// the swap transaction returns the error and does not commit. A failed read
// never becomes a zero count. The swap runs this function before and after
// the swap, so this test holds for both calls.
func TestBreakGlassScopeCountsFailsClosed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		fail    string
		wantRan []string
	}{
		{
			name:    "the list of the namespaces fails",
			fail:    "ListBreakGlassNamespaceScopes",
			wantRan: []string{"ListBreakGlassNamespaceScopes"},
		},
		{
			name:    "the count of a namespace fails",
			fail:    "CountWorkingBreakGlassPrincipals",
			wantRan: []string{"ListBreakGlassNamespaceScopes", "CountWorkingBreakGlassPrincipals"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tx := &scriptTx{fail: tt.fail, rows: []string{"acme", "globex"}, count: 2, ran: nil}

			got, err := breakGlassScopeCounts(t.Context(), db.New(tx), time.UnixMilli(1_700_000_000_000))
			if !errors.Is(err, principal.ErrBreakGlassUnavailable) {
				t.Fatalf("breakGlassScopeCounts = %v, want an error that matches %v",
					err, principal.ErrBreakGlassUnavailable)
			}

			if len(got.names) != 0 || len(got.counts) != 0 {
				t.Fatalf("scopes after the failure = %+v, want none", got)
			}

			if !slices.Equal(tx.ran, tt.wantRan) {
				t.Fatalf("statements = %v, want %v", tx.ran, tt.wantRan)
			}
		})
	}
}

// TestSweepInactiveFailsClosed proves that the inactivity sweep stops at the
// first statement that fails. It returns the error, no result and no skipped
// break-glass account, and runs no later statement: a failed clock read
// disables nothing, and a failed disable or a failed list of the skipped
// break-glass accounts gives the caller nothing to record. The caller then
// returns the error before it appends an audit entry, and the transaction
// does not commit the disables.
func TestSweepInactiveFailsClosed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		fail    string
		wantRan []string
	}{
		{
			name:    "the read of the database clock fails",
			fail:    "DatabaseNowMs",
			wantRan: []string{"DatabaseNowMs"},
		},
		{
			name:    "the disable of the tokens fails",
			fail:    "DisableInactiveAccessTokens",
			wantRan: []string{"DatabaseNowMs", "DisableInactiveAccessTokens"},
		},
		{
			name:    "the disable of the accounts fails",
			fail:    "DisableInactivePrincipals",
			wantRan: []string{"DatabaseNowMs", "DisableInactiveAccessTokens", "DisableInactivePrincipals"},
		},
		{
			name: "the list of the skipped break-glass accounts fails",
			fail: "ListSweepSkippedBreakGlassPrincipals",
			wantRan: []string{
				"DatabaseNowMs", "DisableInactiveAccessTokens", "DisableInactivePrincipals",
				"ListSweepSkippedBreakGlassPrincipals",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tx := &scriptTx{fail: tt.fail, rows: nil, count: 1_700_000_000_000, ran: nil}
			sw := accesstoken.Sweep{Tokens: true, Accounts: true, Inactivity: 90 * 24 * time.Hour}

			res, skipped, err := sweepInactive(t.Context(), db.New(tx), sw)
			if !errors.Is(err, errScriptedStatement) {
				t.Fatalf("sweepInactive = %v, want an error that matches %v", err, errScriptedStatement)
			}

			if !res.At.IsZero() || len(res.Tokens) != 0 || len(res.Principals) != 0 {
				t.Fatalf("result after the failure = %+v, want the zero result", res)
			}

			if len(skipped) != 0 {
				t.Fatalf("skipped after the failure = %v, want none", skipped)
			}

			if !slices.Equal(tx.ran, tt.wantRan) {
				t.Fatalf("statements = %v, want %v", tx.ran, tt.wantRan)
			}
		})
	}
}

// TestSweepInactiveRollsBackOnFailure proves that a failed read or list in
// SweepInactive rolls back every write that ran before it. The test calls the
// body of SweepInactive, which takes the lock of @server in runServerAuditTx.
// The runner of that transaction is pgx.BeginTxFunc, so the rollback is the
// one that the store runs, not a flag in the test. For a failed clock read,
// token disable, account disable or list of skipped break-glass accounts, the
// test checks that no disable and no audit entry is stored and that the entry
// function was not called.
//
// Mutation: replace the runner of sweepInactiveAudited with one that commits
// on error. The account-disable and the skipped-list cases then store a
// disable, and the test fails.
func TestSweepInactiveRollsBackOnFailure(t *testing.T) {
	t.Parallel()

	for _, fail := range []string{
		"DatabaseNowMs",
		"DisableInactiveAccessTokens",
		"DisableInactivePrincipals",
		"ListSweepSkippedBreakGlassPrincipals",
	} {
		t.Run(fail, func(t *testing.T) {
			t.Parallel()

			checkSweepRollsBack(t, fail)
		})
	}
}

// checkSweepRollsBack fails the statement fail in the sweep and checks the
// rollback of the transaction that runServerAuditTx opens.
func checkSweepRollsBack(t *testing.T, fail string) {
	t.Helper()

	tx := &sweepRollbackTx{
		Tx: nil,
		script: &scriptTx{
			fail:  fail,
			rows:  nil,
			count: 1_700_000_000_000,
			ran:   nil,
		},
		pending: nil,
		stored:  nil,
	}
	beginner := sweepRollbackBeginner{tx: tx}
	run := func(ctx context.Context, fn func(q *db.Queries) error) error {
		return pgx.BeginTxFunc(ctx, beginner, pgx.TxOptions{
			IsoLevel:       "",
			AccessMode:     "",
			DeferrableMode: "",
			BeginQuery:     "",
			CommitQuery:    "",
		}, func(pgx.Tx) error {
			return fn(db.New(tx))
		})
	}

	called := false
	entry := func(time.Time, string) audit.Entry {
		called = true

		var e audit.Entry

		return e
	}
	sw := accesstoken.Sweep{Tokens: true, Accounts: true, Inactivity: 90 * 24 * time.Hour}

	res, err := sweepInactiveAudited(t.Context(), run, sw, entry)
	if !errors.Is(err, errScriptedStatement) {
		t.Fatalf("sweepInactiveAudited = %v, want an error that matches %v", err, errScriptedStatement)
	}

	if !res.At.IsZero() || len(res.Tokens) != 0 || len(res.Principals) != 0 {
		t.Fatalf("result after the failure = %+v, want the zero result", res)
	}

	if called {
		t.Fatal("the sweep built an audit entry after a failed statement")
	}

	if len(tx.stored) != 0 {
		t.Fatalf("stored writes after the failure = %v, want none", tx.stored)
	}

	if slices.Contains(tx.script.ran, "InsertAuditEntry") {
		t.Fatalf("statements = %v, want no InsertAuditEntry", tx.script.ran)
	}

	if len(tx.script.ran) == 0 || tx.script.ran[0] != "LockServerAuditScope" {
		t.Fatalf("statements = %v, want the lock of @server first", tx.script.ran)
	}
}

// sweepRollbackBeginner starts the transaction of one sweep test.
type sweepRollbackBeginner struct {
	tx *sweepRollbackTx
}

// BeginTx returns the transaction of the test.
func (b sweepRollbackBeginner) BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error) {
	return b.tx, nil
}

// sweepRollbackTx buffers the writes of the sweep until pgx.BeginTxFunc
// commits them. A rollback discards the buffer, as a real transaction does.
type sweepRollbackTx struct {
	pgx.Tx

	script  *scriptTx
	pending []string
	stored  []string
}

// Exec records one buffered write when the statement succeeds.
func (t *sweepRollbackTx) Exec(
	ctx context.Context, sql string, args ...any,
) (pgconn.CommandTag, error) {
	tag, err := t.script.Exec(ctx, sql, args...)
	if err == nil && isSweepWrite(statementName(sql)) {
		t.pending = append(t.pending, statementName(sql))
	}

	return tag, err
}

// Query records one buffered write when the statement succeeds.
func (t *sweepRollbackTx) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	rows, err := t.script.Query(ctx, sql, args...)
	if err == nil && isSweepWrite(statementName(sql)) {
		t.pending = append(t.pending, statementName(sql))
	}

	return rows, err
}

// QueryRow sends a single-row statement to the script.
func (t *sweepRollbackTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return t.script.QueryRow(ctx, sql, args...)
}

// Commit stores the writes that the sweep buffered.
func (t *sweepRollbackTx) Commit(context.Context) error {
	t.stored = append(t.stored, t.pending...)

	return nil
}

// Rollback discards the writes that the sweep buffered.
func (t *sweepRollbackTx) Rollback(context.Context) error {
	t.pending = nil

	return nil
}

// isSweepWrite reports whether a statement writes a disable or an audit
// entry. The advisory lock of @server is not a stored row.
func isSweepWrite(name string) bool {
	switch name {
	case "DisableInactiveAccessTokens", "DisableInactivePrincipals", "InsertAuditEntry":
		return true
	default:
		return false
	}
}

// ErrStatementFaultForTest is the error of the statement that
// FailStatementForTest fails.
var ErrStatementFaultForTest = errors.New("injected statement failure")

// txBeginner is the pool that guardedPool wraps.
type txBeginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
	BeginTx(ctx context.Context, opts pgx.TxOptions) (pgx.Tx, error)
}

// StatementFaultForTest is a fault that FailStatementForTest set on a store.
type StatementFaultForTest struct {
	s     *Store
	inner txBeginner
	fault *statementFault
}

// FailStatementForTest makes each statement with the given sqlc name fail
// with ErrStatementFaultForTest, in each transaction that s opens after the
// call. The transaction itself is the real one of the store: its rollback
// and its commit run as in production.
func FailStatementForTest(s *Store, name string) StatementFaultForTest {
	inner := s.txs.pool
	fault := &statementFault{name: name, fired: atomic.Int64{}}
	s.txs.pool = statementFaultPool{inner: inner, fault: fault}

	return StatementFaultForTest{s: s, inner: inner, fault: fault}
}

// Fired reports how many times the fault fired.
func (f StatementFaultForTest) Fired() int64 { return f.fault.fired.Load() }

// Restore removes the fault: the transactions that s opens after the call
// run every statement.
func (f StatementFaultForTest) Restore() { f.s.txs.pool = f.inner }

// statementFault names the statement to fail and counts the failures.
type statementFault struct {
	name  string
	fired atomic.Int64
}

// match reports whether sql is the statement to fail, and counts it.
func (f *statementFault) match(sql string) bool {
	if statementName(sql) != f.name {
		return false
	}

	f.fired.Add(1)

	return true
}

// statementFaultPool opens the real transactions of inner and wraps each one
// in a statementFaultTx.
type statementFaultPool struct {
	inner txBeginner
	fault *statementFault
}

// Begin opens a real transaction with the fault.
func (p statementFaultPool) Begin(ctx context.Context) (pgx.Tx, error) {
	tx, err := p.inner.Begin(ctx)
	if err != nil {
		return nil, err
	}

	return statementFaultTx{Tx: tx, fault: p.fault}, nil
}

// BeginTx opens a real transaction of opts with the fault.
func (p statementFaultPool) BeginTx(ctx context.Context, opts pgx.TxOptions) (pgx.Tx, error) {
	tx, err := p.inner.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}

	return statementFaultTx{Tx: tx, fault: p.fault}, nil
}

// statementFaultTx is a real transaction that fails the statement of fault
// and sends each other statement to the database.
type statementFaultTx struct {
	pgx.Tx

	fault *statementFault
}

// Exec fails the faulted statement and runs another one.
func (t statementFaultTx) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	if t.fault.match(sql) {
		return pgconn.CommandTag{}, ErrStatementFaultForTest
	}

	tag, err := t.Tx.Exec(ctx, sql, args...)
	if err != nil {
		return tag, fmt.Errorf("exec: %w", err)
	}

	return tag, nil
}

// Query fails the faulted statement and runs another one.
func (t statementFaultTx) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	if t.fault.match(sql) {
		return nil, ErrStatementFaultForTest
	}

	rows, err := t.Tx.Query(ctx, sql, args...)
	if err != nil {
		return rows, fmt.Errorf("query: %w", err)
	}

	return rows, nil
}

// QueryRow fails the faulted statement and runs another one.
func (t statementFaultTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if t.fault.match(sql) {
		return failedRow{err: ErrStatementFaultForTest}
	}

	return t.Tx.QueryRow(ctx, sql, args...)
}

// scriptTx is a db.DBTX that records the name of each statement and fails
// the statement named fail with errScriptedStatement. Another query returns
// rows, and another single-row query returns count.
type scriptTx struct {
	fail  string
	rows  []string
	count int64
	ran   []string
}

// Exec records the statement and fails the scripted one.
func (d *scriptTx) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	if d.record(sql) {
		return pgconn.CommandTag{}, errScriptedStatement
	}

	return pgconn.CommandTag{}, nil
}

// Query records the statement, fails the scripted one, and returns rows for
// another one.
func (d *scriptTx) Query(_ context.Context, sql string, _ ...any) (pgx.Rows, error) {
	if d.record(sql) {
		return nil, errScriptedStatement
	}

	return &scriptRows{values: d.rows, next: 0}, nil
}

// QueryRow records the statement, fails the scripted one, and returns count
// for another one.
func (d *scriptTx) QueryRow(_ context.Context, sql string, _ ...any) pgx.Row {
	if d.record(sql) {
		return failedRow{err: errScriptedStatement}
	}

	return scriptRow{value: d.count}
}

// record appends the name of sql to ran and reports whether the statement
// must fail.
func (d *scriptTx) record(sql string) bool {
	name := statementName(sql)
	d.ran = append(d.ran, name)

	return name == d.fail
}

// statementName returns the sqlc name of the statement sql, from its first
// line "-- name: <Name> :<kind>".
func statementName(sql string) string {
	rest, ok := strings.CutPrefix(sql, "-- name: ")
	if !ok {
		return sql
	}

	name, _, _ := strings.Cut(rest, " ")

	return name
}

// scriptRow is a pgx.Row that scans value into one *int64.
type scriptRow struct {
	value int64
}

// Scan sets the one destination to value.
func (r scriptRow) Scan(dest ...any) error {
	if len(dest) != 1 {
		return fmt.Errorf("%w: scan %d destinations, want 1", errScriptScan, len(dest))
	}

	p, ok := dest[0].(*int64)
	if !ok {
		return fmt.Errorf("%w: scan into %T, want *int64", errScriptScan, dest[0])
	}

	*p = r.value

	return nil
}

// scriptRows is a pgx.Rows of one text column with the rows values.
type scriptRows struct {
	values []string
	next   int
}

// Close does nothing.
func (*scriptRows) Close() {}

// Err returns nil.
func (*scriptRows) Err() error { return nil }

// CommandTag returns an empty tag.
func (*scriptRows) CommandTag() pgconn.CommandTag { return pgconn.CommandTag{} }

// FieldDescriptions returns nil.
func (*scriptRows) FieldDescriptions() []pgconn.FieldDescription { return nil }

// Next moves to the next value.
func (r *scriptRows) Next() bool {
	if r.next >= len(r.values) {
		return false
	}

	r.next++

	return true
}

// Scan sets the one destination to the current value.
func (r *scriptRows) Scan(dest ...any) error {
	if len(dest) != 1 {
		return fmt.Errorf("%w: scan %d destinations, want 1", errScriptScan, len(dest))
	}

	p, ok := dest[0].(*string)
	if !ok {
		return fmt.Errorf("%w: scan into %T, want *string", errScriptScan, dest[0])
	}

	*p = r.values[r.next-1]

	return nil
}

// Values returns the current value.
func (r *scriptRows) Values() ([]any, error) { return []any{r.values[r.next-1]}, nil }

// RawValues returns the current value as bytes.
func (r *scriptRows) RawValues() [][]byte { return [][]byte{[]byte(r.values[r.next-1])} }

// Conn returns nil: the rows have no connection.
func (*scriptRows) Conn() *pgx.Conn { return nil }

// TypeMap returns nil: the rows decode no values.
func (*scriptRows) TypeMap() *pgtype.Map { return nil }

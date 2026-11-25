package postgres

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"opendustpan.org/opendustpan/internal/store/postgres/internal/db"
	"opendustpan.org/opendustpan/pkg/accesstoken"
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

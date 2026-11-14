package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"opendustpan.org/opendustpan/pkg/backup"
)

// errNoQuery is the error of the fake querier below: a query must not run.
var errNoQuery = errors.New("no query may run")

// failQuerier fails the test when the code under test runs a query. The
// values it returns are never read.
type failQuerier struct {
	t *testing.T
}

func (f failQuerier) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	f.t.Helper()
	f.t.Fatalf("a query ran, want no query")

	return pgconn.CommandTag{}, errNoQuery
}

func (f failQuerier) QueryRow(context.Context, string, ...any) pgx.Row {
	f.t.Helper()
	f.t.Fatalf("a query ran, want no query")

	return failRow{}
}

func (f failQuerier) Query(context.Context, string, ...any) (pgx.Rows, error) {
	f.t.Helper()
	f.t.Fatalf("a query ran, want no query")

	return failRows{Rows: nil}, errNoQuery
}

// failRow is a pgx.Row whose scan reports that no query may run.
type failRow struct{}

func (failRow) Scan(...any) error { return errNoQuery }

// failRows is a pgx.Rows with no rows. The fake querier fails the test
// before a method of it runs.
type failRows struct {
	pgx.Rows
}

// TestCheckStagedRefusesTheStagingIdentFirst proves that checkStaged checks
// the staging name before it runs a query: the fake querier fails the test on
// any query. The valid case goes to checkStagingIdent alone, because
// checkStaged then reads the staging schema.
func TestCheckStagedRefusesTheStagingIdentFirst(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		staging string
		wantErr bool
	}{
		{"a name with a dash", "bad-name", true},
		{"a name that starts with a digit", "1abc", true},
		{"a name that checkStagingIdent accepts", "staging_0a1b", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if !tt.wantErr {
				if err := checkStagingIdent(tt.staging); err != nil {
					t.Fatalf("checkStagingIdent(%q) = %v, want nil", tt.staging, err)
				}

				return
			}

			err := checkStaged(t.Context(), failQuerier{t: t}, tt.staging, time.Time{})
			if !errors.Is(err, backup.ErrCorrupt) {
				t.Fatalf("checkStaged(%q) = %v, want ErrCorrupt", tt.staging, err)
			}
		})
	}
}

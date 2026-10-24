package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// conflictOnRead replaces the table of s with a view whose read fails with
// SQLSTATE 40001, a serialization failure.
func conflictOnRead(t *testing.T, s *Store, table string) {
	t.Helper()

	ctx := t.Context()
	name := pgx.Identifier{table}.Sanitize()
	kept := pgx.Identifier{table + "_real"}.Sanitize()
	statements := []string{
		"CREATE FUNCTION conflict_id() RETURNS text LANGUAGE plpgsql AS " +
			"$$ BEGIN RAISE EXCEPTION 'test conflict' USING ERRCODE = '40001'; END $$",
		"ALTER TABLE " + name + " RENAME TO " + kept,
		"CREATE VIEW " + name + " AS SELECT conflict_id() AS id",
	}

	for _, statement := range statements {
		if _, err := s.pool.Exec(ctx, statement); err != nil {
			t.Fatalf("set up the conflict on %s: %v", table, err)
		}
	}
}

// TestStateAtReportsReadConflictAsUnavailable proves that a deadlock or a
// serialization failure in the reads of StateAt and WorkspaceStateAt reports
// namespace.ErrUnavailable with no driver text.
func TestStateAtReportsReadConflictAsUnavailable(t *testing.T) {
	t.Parallel()

	at := time.UnixMilli(1_700_000_000_000).UTC()
	cases := []struct {
		name  string
		table string
		read  func(ctx context.Context, s *Store) error
	}{
		{name: "stack", table: "stacks", read: func(ctx context.Context, s *Store) error {
			_, err := s.StateAt(ctx, stack.ID("stk-missing"), at)
			return err
		}},
		{name: "workspace", table: "workspaces", read: func(ctx context.Context, s *Store) error {
			_, err := s.WorkspaceStateAt(ctx, workspace.ID("ws-missing"), at)
			return err
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := openKeepLockStore(t, testPostgresURL(t))
			conflictOnRead(t, s, tc.table)

			err := tc.read(t.Context(), s)
			if !errors.Is(err, namespace.ErrUnavailable) || strings.Contains(err.Error(), "test conflict") {
				t.Fatalf("read = %v, want namespace.ErrUnavailable without driver text", err)
			}
		})
	}
}

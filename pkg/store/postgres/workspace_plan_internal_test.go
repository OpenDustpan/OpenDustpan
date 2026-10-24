package postgres

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
)

// sqlRecorder is a pgx query tracer that keeps the text of the last statement
// that its connection sends.
type sqlRecorder struct {
	sql string
}

// TraceQueryStart implements pgx.QueryTracer.
func (r *sqlRecorder) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	r.sql = data.SQL

	return ctx
}

// TraceQueryEnd implements pgx.QueryTracer.
func (*sqlRecorder) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

// TestWorkspaceByIDReadsUseThePrimaryKey proves that each read and write of
// one live workspace by its ID uses the primary key. The partial index of the
// live workspace names must not serve them: on a database with no statistics
// and with a generic plan, that index can win, and then each call reads the
// whole index. pgx caches a plan for each connection, so the test explains the
// generic plan of each statement on a fresh database.
func TestWorkspaceByIDReadsUseThePrimaryKey(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := openKeepLockStore(t, testPostgresURL(t))
	recorder := &sqlRecorder{sql: ""}

	cfg := s.pool.Config().ConnConfig.Copy()
	cfg.Tracer = recorder

	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := conn.Close(context.WithoutCancel(ctx)); closeErr != nil {
			t.Errorf("close: %v", closeErr)
		}
	})

	if _, err = conn.Exec(ctx, "SET plan_cache_mode = force_generic_plan"); err != nil {
		t.Fatalf("set plan cache mode: %v", err)
	}

	q := db.New(conn)

	for i, tc := range []struct {
		send func() error
		name string
		args string
	}{
		{name: "ReadWorkspaceByID", args: "('wsp-x')", send: func() error {
			_, readErr := q.ReadWorkspaceByID(ctx, "wsp-x")

			return readErr
		}},
		{name: "GetWorkspaceByID", args: "('wsp-x')", send: func() error {
			_, readErr := q.GetWorkspaceByID(ctx, "wsp-x")

			return readErr
		}},
		{name: "LockWorkspaceForFinalize", args: "('wsp-x')", send: func() error {
			_, readErr := q.LockWorkspaceForFinalize(ctx, "wsp-x")

			return readErr
		}},
		{name: "LockWorkspaceForDeclare", args: "('wsp-x')", send: func() error {
			_, readErr := q.LockWorkspaceForDeclare(ctx, "wsp-x")

			return readErr
		}},
		{name: "WorkspaceExists", args: "('wsp-x')", send: func() error {
			_, readErr := q.WorkspaceExists(ctx, "wsp-x")

			return readErr
		}},
		{name: "SoftDeleteWorkspace", args: "(1, 'wsp-x')", send: func() error {
			_, writeErr := q.SoftDeleteWorkspace(ctx, db.SoftDeleteWorkspaceParams{
				DeletedAtMs: pgtype.Int8{Int64: 1, Valid: true}, ID: "wsp-x",
			})

			return writeErr
		}},
	} {
		// The database holds no workspace, so a read finds no row.
		if sendErr := tc.send(); sendErr != nil && !errors.Is(sendErr, pgx.ErrNoRows) {
			t.Fatalf("%s: %v", tc.name, sendErr)
		}

		plan, planErr := genericPlan(ctx, conn, "workspace_plan_"+strconv.Itoa(i), recorder.sql, tc.args)
		if planErr != nil {
			t.Fatalf("%s: %v", tc.name, planErr)
		}

		if !strings.Contains(plan, "workspaces_pkey") || strings.Contains(plan, "workspaces_live_name") {
			t.Errorf("%s: the generic plan does not use the primary key:\n%s", tc.name, plan)
		}
	}
}

// genericPlan prepares statement as name on conn and returns the text of the
// plan of its execution with args, as conn plans it.
func genericPlan(ctx context.Context, conn *pgx.Conn, name, statement, args string) (string, error) {
	if _, err := conn.Exec(ctx, "PREPARE "+name+" AS "+statement); err != nil {
		return "", fmt.Errorf("prepare: %w", err)
	}

	rows, err := conn.Query(ctx, "EXPLAIN EXECUTE "+name+args)
	if err != nil {
		return "", fmt.Errorf("explain: %w", err)
	}

	lines, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return "", fmt.Errorf("read the plan: %w", err)
	}

	return strings.Join(lines, "\n"), nil
}

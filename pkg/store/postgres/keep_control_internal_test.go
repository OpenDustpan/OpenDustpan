package postgres

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"testing"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/store/internal/migrate"
)

// connectKeepControl connects to the test database and closes the
// connection after the test.
func connectKeepControl(t *testing.T) *pgx.Conn {
	t.Helper()

	ctx := t.Context()

	conn, err := pgx.Connect(ctx, testPostgresURL(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := conn.Close(context.WithoutCancel(ctx)); closeErr != nil {
			t.Errorf("close: %v", closeErr)
		}
	})

	return conn
}

// durableSchema creates a schema with the baseline tables on conn and holds
// one operation with key and token in it, with one step, one schedule
// called key whose next run is token, and one sign-in failure window of key
// that ends at token with count token.
func durableSchema(t *testing.T, conn *pgx.Conn, key string, token int64) string {
	t.Helper()

	ctx := t.Context()

	suffix := make([]byte, 8)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatalf("random schema name: %v", err)
	}

	name := "test_" + hex.EncodeToString(suffix)
	ident := pgx.Identifier{name}.Sanitize()

	all, err := migrate.Load(migrations, "migrations")
	if err != nil {
		t.Fatalf("load migrations: %v", err)
	}

	stmts := make([]string, 0, len(all)+2)
	stmts = append(stmts, "CREATE SCHEMA "+ident,
		"SET search_path TO "+ident+", "+pgx.Identifier{name + "_control"}.Sanitize())
	for _, m := range all {
		stmts = append(stmts, m.SQL)
	}

	dropSchemaAtCleanup(t, testPostgresURL(t), name)

	for _, stmt := range stmts {
		if _, execErr := conn.Exec(ctx, stmt); execErr != nil {
			t.Fatalf("create durable schema: %v", execErr)
		}
	}

	insertOp := "INSERT INTO durable_operations (id, start_key, kind, input, version, state, attempt, last_error," +
		" claim_owner, claim_token, claim_expires_at_ms, cancel_requested, not_before_ms, created_at_ms, updated_at_ms)" +
		" VALUES ($1, $2, 'kind-a', '', 0, 'running', $3, '', 'instance-a', $3, 9999999999999, false, 0, 0, 0)"
	if _, execErr := conn.Exec(ctx, insertOp, "op-"+key, key, token); execErr != nil {
		t.Fatalf("insert operation: %v", execErr)
	}

	insertStep := "INSERT INTO durable_steps (operation_id, name, seq, result, done_at_ms) VALUES ($1, 'one', 1, '', 0)"
	if _, execErr := conn.Exec(ctx, insertStep, "op-"+key); execErr != nil {
		t.Fatalf("insert step: %v", execErr)
	}

	insertSchedule := "INSERT INTO durable_schedules (name, interval_ms, next_run_at_ms) VALUES ($1, 1000, $2)"
	if _, execErr := conn.Exec(ctx, insertSchedule, key, token); execErr != nil {
		t.Fatalf("insert schedule: %v", execErr)
	}

	insertFailure := "INSERT INTO sign_in_failures (source, window_end_ms, count) VALUES ($1, $2, $2)"
	if _, execErr := conn.Exec(ctx, insertFailure, key, token); execErr != nil {
		t.Fatalf("insert sign-in failure: %v", execErr)
	}

	insertAuditChainRows(t, conn, key, token)

	if _, execErr := conn.Exec(ctx, "RESET search_path"); execErr != nil {
		t.Fatalf("reset search path: %v", execErr)
	}

	return name
}

// insertAuditChainRows adds the audit chain check row and the mark of scope
// key to the schema on the search path of conn.
func insertAuditChainRows(t *testing.T, conn *pgx.Conn, key string, token int64) {
	t.Helper()

	ctx := t.Context()

	insertCheck := "INSERT INTO audit_chain_check (id, checked_at_ms, invalid) VALUES (1, $1, $1)"
	if _, execErr := conn.Exec(ctx, insertCheck, token); execErr != nil {
		t.Fatalf("insert audit chain check: %v", execErr)
	}

	insertMark := "INSERT INTO audit_chain_mark (scope, epoch, sequence, marked_at_ms) VALUES ($1, $2, $2, $2)"
	if _, execErr := conn.Exec(ctx, insertMark, key, token); execErr != nil {
		t.Fatalf("insert audit chain mark: %v", execErr)
	}
}

// TestKeepControlTablesKeepsTheLiveRows proves that the swap of a restore
// keeps the durable operations of the live installation: the restored schema
// ends with the rows of the previous schema, with their claim tokens, and
// without the rows of the backup. A restored token below the live one would
// let a stale holder write.
func TestKeepControlTablesKeepsTheLiveRows(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	conn := connectKeepControl(t)
	live := durableSchema(t, conn, "from-backup", 3)
	previous := durableSchema(t, conn, "live", 5)

	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	if keepErr := keepControlTables(ctx, tx, live, previous); keepErr != nil {
		_ = tx.Rollback(ctx) //nolint:errcheck // The test fails on the keep error.
		t.Fatalf("keep control tables: %v", keepErr)
	}

	if commitErr := tx.Commit(ctx); commitErr != nil {
		t.Fatalf("commit: %v", commitErr)
	}

	ident := pgx.Identifier{live}.Sanitize()

	var (
		key   string
		token int64
		count int
		steps int
	)

	read := "SELECT start_key, claim_token, (SELECT count(*) FROM " + ident + ".durable_operations)," +
		" (SELECT count(*) FROM " + ident + ".durable_steps WHERE operation_id = 'op-live')" +
		" FROM " + ident + ".durable_operations"
	if scanErr := conn.QueryRow(ctx, read).Scan(&key, &token, &count, &steps); scanErr != nil {
		t.Fatalf("read restored operations: %v", scanErr)
	}

	if key != "live" || token != 5 || count != 1 || steps != 1 {
		t.Fatalf("restored operations = key %q token %d (%d rows, %d steps), want the live key at token 5, "+
			"one row and its step", key, token, count, steps)
	}

	checkKeptSchedules(t, conn, ident)

	checkKeptSignInFailures(t, conn, ident)

	checkKeptAuditChainCheck(t, conn, ident)
}

// checkKeptAuditChainCheck fails t unless the schema ident holds only the
// live audit chain mark row, with the mark that it had before the swap: a
// restore must not clear the mark of a checkpoint mismatch.
func checkKeptAuditChainCheck(t *testing.T, conn *pgx.Conn, ident string) {
	t.Helper()

	var (
		scope string
		epoch int64
		seq   int64
		rows  int
	)

	read := "SELECT scope, epoch, sequence, (SELECT count(*) FROM " + ident +
		".audit_chain_mark) FROM " + ident + ".audit_chain_mark"
	if scanErr := conn.QueryRow(t.Context(), read).Scan(&scope, &epoch, &seq, &rows); scanErr != nil {
		t.Fatalf("read restored audit chain check: %v", scanErr)
	}

	if scope != "live" || epoch != 5 || seq != 5 || rows != 1 {
		t.Fatalf("restored audit chain check: scope %q epoch %d sequence %d rows %d", scope, epoch, seq, rows)
	}
}

// checkKeptSchedules fails t unless the schema ident holds only the live
// schedule, with the next run that it had before the swap.
func checkKeptSchedules(t *testing.T, conn *pgx.Conn, ident string) {
	t.Helper()

	var (
		schedule string
		nextRun  int64
		rows     int
	)

	readSchedule := "SELECT name, next_run_at_ms, (SELECT count(*) FROM " + ident + ".durable_schedules) FROM " +
		ident + ".durable_schedules"
	if scanErr := conn.QueryRow(t.Context(), readSchedule).Scan(&schedule, &nextRun, &rows); scanErr != nil {
		t.Fatalf("read restored schedules: %v", scanErr)
	}

	if schedule != "live" || nextRun != 5 || rows != 1 {
		t.Fatalf("restored schedules = %q next run %d (%d rows), want only the live schedule with next run 5",
			schedule, nextRun, rows)
	}
}

// checkKeptSignInFailures fails t unless the schema ident holds only the live
// sign-in failure window, with the end and the count that it had before the
// swap.
func checkKeptSignInFailures(t *testing.T, conn *pgx.Conn, ident string) {
	t.Helper()

	var (
		source string
		end    int64
		count  int64
		rows   int
	)

	read := "SELECT source, window_end_ms, count, (SELECT count(*) FROM " + ident + ".sign_in_failures) FROM " +
		ident + ".sign_in_failures"
	if scanErr := conn.QueryRow(t.Context(), read).Scan(&source, &end, &count, &rows); scanErr != nil {
		t.Fatalf("read restored sign-in failures: %v", scanErr)
	}

	if source != "live" || end != 5 || count != 5 || rows != 1 {
		t.Fatalf("restored sign-in failures: source %q end %d count %d rows %d", source, end, count, rows)
	}
}

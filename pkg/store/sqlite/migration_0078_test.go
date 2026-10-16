package sqlite_test

import (
	"database/sql"
	"path/filepath"
	"testing"

	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// TestMigration0078FailsStaleRunningRows proves that the migration of the
// one-running indexes opens a database that holds two running backups and
// two running restores: it marks them failed, as a server start does, and
// then builds the indexes.
func TestMigration0078FailsStaleRunningRows(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "dustpan.db")

	s, err := sqlite.Open(t.Context(), path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	if closeErr := s.Close(); closeErr != nil {
		t.Fatalf("close store: %v", closeErr)
	}

	raw, err := openRawDB(path)
	if err != nil {
		t.Fatal(err)
	}

	defer func() {
		if closeErr := raw.Close(); closeErr != nil {
			t.Errorf("close raw database: %v", closeErr)
		}
	}()

	// Back to the schema before the migration, with the rows that a race
	// or a failed finish write leaves.
	execAll(
		t,
		raw,
		`DROP INDEX backups_one_running`,
		`DROP INDEX restores_one_running`,
		`DELETE FROM schema_migrations WHERE version = 78`,
		`INSERT INTO backups (id, state, started_at_ms, target) VALUES ('bk-1', 'running', 1, 'fs'), ('bk-2', 'running', 2, 'fs')`,
		`INSERT INTO restores (id, state, backup_id, started_at_ms) VALUES
			('rs-1', 'checking', 'bk-0', 1), ('rs-2', 'applying', 'bk-0', 2)`,
	)

	reopened, err := sqlite.Open(t.Context(), path)
	if err != nil {
		t.Fatalf("open a database with two running backups and two running restores: %v", err)
	}

	if closeErr := reopened.Close(); closeErr != nil {
		t.Fatalf("close store: %v", closeErr)
	}

	checkStaleRowsFailed(t, raw, `SELECT state, error, ended_at_ms FROM backups ORDER BY id`,
		"server stopped during backup")
	checkStaleRowsFailed(t, raw, `SELECT state, error, ended_at_ms FROM restores ORDER BY id`,
		"server stopped during restore")

	const twoRunning = `INSERT INTO backups (id, state, started_at_ms, target)
		VALUES ('bk-3', 'running', 3, 'fs'), ('bk-4', 'running', 4, 'fs')`

	if _, err := raw.ExecContext(t.Context(), twoRunning); err == nil {
		t.Fatal("insert two running backups after the migration: err = nil, want the unique index to refuse")
	}
}

func execAll(t *testing.T, raw *sql.DB, statements ...string) {
	t.Helper()

	for _, stmt := range statements {
		if _, err := raw.ExecContext(t.Context(), stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
}

// checkStaleRowsFailed fails the test unless query gives two rows, each
// failed with errText and an end time.
func checkStaleRowsFailed(t *testing.T, raw *sql.DB, query, errText string) {
	t.Helper()

	rows, err := raw.QueryContext(t.Context(), query)
	if err != nil {
		t.Fatalf("read rows: %v", err)
	}

	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			t.Errorf("close rows: %v", closeErr)
		}
	}()

	n := 0

	for rows.Next() {
		var (
			state, gotErr string
			endedAt       int64
		)

		if scanErr := rows.Scan(&state, &gotErr, &endedAt); scanErr != nil {
			t.Fatalf("scan row: %v", scanErr)
		}

		if state != "failed" || gotErr != errText || endedAt <= 0 {
			t.Fatalf("row after the migration = %s, %q, ended %d; want failed, %q and an end time",
				state, gotErr, endedAt, errText)
		}

		n++
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		t.Fatalf("read rows: %v", rowsErr)
	}

	if n != 2 {
		t.Fatalf("rows after the migration = %d, want 2", n)
	}
}

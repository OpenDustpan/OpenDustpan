//go:build e2e

package e2e_test

import (
	"context"
	"database/sql"
	"net/url"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/e2e"
)

// damagedJob is one job row that the step writes: its ID and a body that does
// not decode.
type damagedJob struct {
	id   string
	body string
}

// damagedJobRows returns the rows of the step: text that is not JSON, and an
// empty object.
func damagedJobRows() []damagedJob {
	return []damagedJob{{id: "job-dmg1", body: "not json"}, {id: "job-dmg2", body: "{}"}}
}

// insertDamagedJobRows writes the damaged job rows to the database of the
// server, for the unit unitID of the namespace of the harness, with direct
// SQL. No API route writes a body that does not decode.
func insertDamagedJobRows(t *testing.T, env *e2e.Env, unitID string) {
	t.Helper()

	dsn := env.DatabaseURL(t)

	if path, ok := strings.CutPrefix(dsn, "sqlite:"); ok {
		insertDamagedJobRowsSQLite(t, path, unitID)

		return
	}

	insertDamagedJobRowsPostgres(t, dsn, unitID)
}

// insertDamagedJobRowsSQLite writes the rows to the SQLite file at path.
func insertDamagedJobRowsSQLite(t *testing.T, path, unitID string) {
	t.Helper()

	query := url.Values{}
	query.Add("_pragma", "busy_timeout(10000)")

	conn, err := sql.Open("sqlite", (&url.URL{Scheme: "file", Path: path, RawQuery: query.Encode()}).String())
	if err != nil {
		t.Fatalf("open the database: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(); closeErr != nil {
			t.Errorf("close the database: %v", closeErr)
		}
	}()

	for i, row := range damagedJobRows() {
		_, err = conn.ExecContext(t.Context(), `INSERT INTO jobs (id, namespace_id, unit_id, seq, state, wait,
current_phase, current_changes, current_operation, idempotency_key, version, body, created_at_ms, updated_at_ms,
state_changed_at_ms)
SELECT ?, id, ?, ?, 'queued', '', 0, 0, '', '', 1, ?, 1700000000000, 1700000000000, 1700000000000
FROM namespaces WHERE name = ?`, row.id, unitID, 9000+i, row.body, e2e.Namespace)
		if err != nil {
			t.Fatalf("insert the damaged job %s: %v", row.id, err)
		}
	}
}

// insertDamagedJobRowsPostgres writes the rows to the PostgreSQL database at
// dsn.
func insertDamagedJobRowsPostgres(t *testing.T, dsn, unitID string) {
	t.Helper()

	ctx := t.Context()

	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect to the database: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(context.WithoutCancel(ctx)); closeErr != nil {
			t.Errorf("close the database: %v", closeErr)
		}
	}()

	for i, row := range damagedJobRows() {
		_, err = conn.Exec(ctx, `INSERT INTO jobs (id, namespace_id, unit_id, seq, state, wait, current_phase,
current_changes, current_operation, idempotency_key, version, body, created_at_ms, updated_at_ms, state_changed_at_ms)
SELECT $1, id, $2, $3, 'queued', '', 0, FALSE, '', '', 1, $4, 1700000000000, 1700000000000, 1700000000000
FROM namespaces WHERE name = $5`, row.id, unitID, 9000+i, row.body, e2e.Namespace)
		if err != nil {
			t.Fatalf("insert the damaged job %s: %v", row.id, err)
		}
	}
}

// TestBackupCheckReportsADamagedJob proves that backup check and restore agree
// on a backup that holds a damaged job row: backup check exits 1 and names the
// job, the unit and the reason, and a restore, with and without --check-only,
// exits 1 with the same text. It runs only with E2E_SERVER=binary, on every
// store under test.
func TestBackupCheckReportsADamagedJob(t *testing.T) {
	t.Parallel()

	if !e2e.UseBinaryServer() {
		t.Skip("needs E2E_SERVER=binary")
	}

	fetcher := sharedFetcher(t)

	databases, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	version := e2e.Pulumi().Single()
	if version == "" {
		t.Fatal("the Pulumi tool needs at least one version in its matrix")
	}

	installCtx, cancel := context.WithTimeout(t.Context(), installLimit)
	t.Cleanup(cancel)

	cli, err := fetcher.Install(installCtx, e2e.Pulumi(), version)
	if err != nil {
		t.Fatalf("install %s %s: %v", e2e.Pulumi().Name, version, err)
	}

	for _, db := range databases {
		t.Run(string(db), func(t *testing.T) {
			t.Parallel()
			runDamagedJobBackupOnStore(t, cli, db)
		})
	}
}

// runDamagedJobBackupOnStore runs the damaged job steps on one store.
func runDamagedJobBackupOnStore(t *testing.T, cli e2e.Install, store e2e.Store) {
	t.Helper()

	if store == e2e.StorePostgres && e2e.PostgresURL() == "" {
		t.Skip("set DUSTPAN_TEST_POSTGRES_URL to run the PostgreSQL suite")
	}

	ctx := t.Context()
	env := e2e.NewEnv(ctx, t, cli, store)

	insertDamagedJobRows(t, env, "unit-dmg")

	id := startBackupForCheck(ctx, t, env)

	check := env.RunBinary(
		ctx, t, "backup", "check", "--server", env.URL, "--ca-file", env.CABundle(), "--backup", id,
	)
	if check.ExitCode != 1 {
		t.Fatalf("backup check exits with status %d, want 1:\n%s%s", check.ExitCode, check.Stdout, check.Stderr)
	}

	out := check.Stdout + check.Stderr
	for _, want := range []string{"damaged job job-dmg", "of unit unit-dmg", "cancel the damaged job"} {
		if !strings.Contains(out, want) {
			t.Fatalf("backup check output lacks %q:\n%s", want, out)
		}
	}

	if strings.Contains(out, "is complete") {
		t.Fatalf("backup check calls a backup with a damaged job complete:\n%s", out)
	}

	for _, extra := range [][]string{{"--check-only"}, nil} {
		args := append([]string{"restore", "--server", env.URL, "--ca-file", env.CABundle(), "--backup", id}, extra...)

		res := env.RunBinary(ctx, t, args...)
		if res.ExitCode != 1 {
			t.Fatalf("restore %v exits with status %d, want 1:\n%s%s", extra, res.ExitCode, res.Stdout, res.Stderr)
		}

		restoreOut := res.Stdout + res.Stderr
		for _, want := range []string{"damaged job job-dmg", "of unit unit-dmg", "cancel the damaged job"} {
			if !strings.Contains(restoreOut, want) {
				t.Fatalf("restore %v output lacks %q:\n%s", extra, want, restoreOut)
			}
		}
	}
}

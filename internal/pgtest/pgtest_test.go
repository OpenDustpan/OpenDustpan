package pgtest_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/internal/pgtest"
)

const envURL = "DUSTPAN_TEST_POSTGRES_URL"

// recorder is a testing.TB that keeps its cleanups, logs and failures, so
// that a test can run the cleanup of a helper and read the result.
type recorder struct {
	testing.TB

	mu       sync.Mutex
	cleanups []func()
	logs     []string
	errors   []string
}

func newRecorder(tb testing.TB) *recorder {
	tb.Helper()

	return &recorder{TB: tb, mu: sync.Mutex{}, cleanups: nil, logs: nil, errors: nil}
}

func (r *recorder) Cleanup(f func()) { r.cleanups = append(r.cleanups, f) }

func (r *recorder) Logf(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.logs = append(r.logs, fmt.Sprintf(format, args...))
}

func (r *recorder) Errorf(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.errors = append(r.errors, fmt.Sprintf(format, args...))
}

func (r *recorder) runCleanups() {
	for _, f := range slices.Backward(r.cleanups) {
		f()
	}
}

func newTemplate(t *testing.T, base string) string {
	t.Helper()

	suffix := make([]byte, 6)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatalf("random name: %v", err)
	}

	// The name has the shape that the stale cleanup of the store tests parses:
	// the prefix, the Unix time, and a suffix. A template that a crash leaves
	// is then dropped by a later run.
	name := fmt.Sprintf("dustpan_test_%d_pgtest%s", time.Now().Unix(), hex.EncodeToString(suffix))

	migrate := func(ctx context.Context, dbURL string) error {
		conn, err := pgx.Connect(ctx, dbURL)
		if err != nil {
			return fmt.Errorf("connect: %w", err)
		}

		defer func() { _ = conn.Close(ctx) }() //nolint:errcheck // Test setup.

		if _, err = conn.Exec(ctx, "CREATE TABLE t (id int)"); err != nil {
			return fmt.Errorf("create table: %w", err)
		}

		return nil
	}

	if err := pgtest.BuildTemplate(t.Context(), base, name, migrate); err != nil {
		t.Fatalf("build the template: %v", err)
	}

	t.Cleanup(func() {
		conn, err := pgx.Connect(context.Background(), base)
		if err != nil {
			t.Logf("connect: %v", err)

			return
		}

		defer func() { _ = conn.Close(context.Background()) }() //nolint:errcheck // Test cleanup.

		drop := "DROP DATABASE IF EXISTS " + pgx.Identifier{name}.Sanitize() + " WITH (FORCE)"
		if _, err = conn.Exec(context.Background(), drop); err != nil {
			t.Logf("drop the template: %v", err)
		}
	})

	// Cleanups run last in, first out: this one drops the clones before the
	// cleanup above drops the template, also when the test ends with a Fatal.
	t.Cleanup(func() {
		if _, err := pgtest.DropClones(context.Background(), base, name, t.Logf); err != nil {
			t.Logf("drop the clones: %v", err)
		}
	})

	return name
}

func countClones(t *testing.T, base, template string) int {
	t.Helper()

	conn, err := pgx.Connect(t.Context(), base)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	defer func() { _ = conn.Close(t.Context()) }() //nolint:errcheck // Test read.

	var n int
	query := "SELECT count(*) FROM pg_database WHERE starts_with(datname, $1)"
	if err = conn.QueryRow(t.Context(), query, template+"_c").Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}

	return n
}

func TestFailedDropDoesNotFailTheTest(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("%s is not set", envURL)
	}

	template := newTemplate(t, base)
	rec := newRecorder(t)

	// A time limit of one nanosecond makes the drop fail at once.
	_ = pgtest.MigratedURLWithTimeout(rec, base, template, time.Nanosecond)

	if got := countClones(t, base, template); got != 1 {
		t.Fatalf("clones after the clone: got %d, want 1", got)
	}

	rec.runCleanups()

	if len(rec.errors) != 0 {
		t.Fatalf("a failed drop failed the test: %v", rec.errors)
	}

	if len(rec.logs) != 1 || !strings.Contains(rec.logs[0], "drop the clone") {
		t.Fatalf("logs: got %v, want one line about the drop", rec.logs)
	}

	if got := countClones(t, base, template); got != 1 {
		t.Fatalf("clones after the failed drop: got %d, want 1", got)
	}

	assertSweep(t, base, template)
}

// assertSweep checks that the sweep counts and logs a drop that fails, and
// that a sweep with the normal time limit then removes the clone.
func assertSweep(t *testing.T, base, template string) {
	t.Helper()

	var lines []string

	failed, err := pgtest.DropClonesWithTimeout(t.Context(), base, template, time.Nanosecond,
		func(format string, args ...any) { lines = append(lines, fmt.Sprintf(format, args...)) })
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}

	if failed != 1 || len(lines) != 1 {
		t.Fatalf("failed sweep: got %d failures and %d lines, want 1 and 1", failed, len(lines))
	}

	// The sweep with the normal time limit drops the clone.
	failed, err = pgtest.DropClones(t.Context(), base, template, func(string, ...any) {})
	if err != nil || failed != 0 {
		t.Fatalf("sweep: got %d failures and error %v, want none", failed, err)
	}

	if got := countClones(t, base, template); got != 0 {
		t.Fatalf("clones after the sweep: got %d, want 0", got)
	}
}

func TestDropClonesKeepsOtherTemplates(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("%s is not set", envURL)
	}

	mine := newTemplate(t, base)
	other := newTemplate(t, base)
	recMine := newRecorder(t)
	recOther := newRecorder(t)

	_ = pgtest.MigratedURLWithTimeout(recMine, base, mine, time.Nanosecond)
	_ = pgtest.MigratedURLWithTimeout(recOther, base, other, time.Nanosecond)

	if failed, err := pgtest.DropClones(t.Context(), base, mine, func(string, ...any) {}); err != nil || failed != 0 {
		t.Fatalf("sweep: got %d failures and error %v, want none", failed, err)
	}

	if got := countClones(t, base, mine); got != 0 {
		t.Fatalf("clones of this run: got %d, want 0", got)
	}

	if got := countClones(t, base, other); got != 1 {
		t.Fatalf("clones of the other run: got %d, want 1", got)
	}

	if _, err := pgtest.DropClones(t.Context(), base, other, func(string, ...any) {}); err != nil {
		t.Fatalf("sweep: %v", err)
	}
}

func TestMissingCloneStillFails(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("%s is not set", envURL)
	}

	// The template does not exist, so the clone fails. The failure must reach
	// the test.
	fatal := make(chan string, 1)
	rec := &fatalRecorder{recorder: newRecorder(t), fatal: fatal}

	done := make(chan struct{})

	go func() {
		defer close(done)

		pgtest.MigratedURLWithTimeout(rec, base, "dustpan_test_pgtest_missing", time.Second)
	}()

	<-done

	select {
	case msg := <-fatal:
		if !strings.Contains(msg, "clone the template database") {
			t.Fatalf("message: %q", msg)
		}
	default:
		t.Fatal("a clone that cannot be made did not fail the test")
	}
}

// fatalRecorder turns Fatalf into a message and ends the goroutine, as
// testing.T does.
type fatalRecorder struct {
	*recorder

	fatal chan string
}

func (f *fatalRecorder) Fatalf(format string, args ...any) {
	f.fatal <- fmt.Sprintf(format, args...)

	runtime.Goexit()
}

func TestDropSchemaAtCleanupDropsBothSchemas(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("%s is not set", envURL)
	}

	suffix := make([]byte, 6)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatalf("random name: %v", err)
	}

	name := "pgtest_drop_" + hex.EncodeToString(suffix)
	schemas := []string{name, name + "_control"}

	createSchemas(t, base, schemas)

	// The test drops the schemas too when it fails before the check, so a
	// failed run leaves nothing.
	t.Cleanup(func() { dropSchemas(t, base, schemas) })

	rec := newRecorder(t)
	pgtest.DropSchemaAtCleanup(rec, base, name)
	rec.runCleanups()

	if len(rec.errors) != 0 {
		t.Fatalf("cleanup errors: %v", rec.errors)
	}

	if got := countSchemas(t, base, schemas); got != 0 {
		t.Fatalf("schemas after the cleanup: got %d, want 0", got)
	}
}

// createSchemas makes each schema of schemas on the server of base.
func createSchemas(t *testing.T, base string, schemas []string) {
	t.Helper()

	conn, err := pgx.Connect(t.Context(), base)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	for _, schema := range schemas {
		stmt := "CREATE SCHEMA " + pgx.Identifier{schema}.Sanitize()
		if _, execErr := conn.Exec(t.Context(), stmt); execErr != nil {
			t.Fatalf("create schema: %v", execErr)
		}
	}

	if closeErr := conn.Close(t.Context()); closeErr != nil {
		t.Fatalf("close: %v", closeErr)
	}
}

// dropSchemas drops each schema of schemas. It logs a failure, so that the
// cleanup of the test reports it without a second failure.
func dropSchemas(t *testing.T, base string, schemas []string) {
	t.Helper()

	conn, err := pgx.Connect(context.Background(), base)
	if err != nil {
		t.Logf("connect: %v", err)

		return
	}

	defer func() {
		if closeErr := conn.Close(context.Background()); closeErr != nil {
			t.Logf("close: %v", closeErr)
		}
	}()

	for _, schema := range schemas {
		drop := "DROP SCHEMA IF EXISTS " + pgx.Identifier{schema}.Sanitize() + " CASCADE"
		if _, execErr := conn.Exec(context.Background(), drop); execErr != nil {
			t.Logf("drop schema %s: %v", schema, execErr)
		}
	}
}

// countSchemas answers how many of schemas exist on the server of base.
func countSchemas(t *testing.T, base string, schemas []string) int {
	t.Helper()

	conn, err := pgx.Connect(t.Context(), base)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(t.Context()); closeErr != nil {
			t.Errorf("close: %v", closeErr)
		}
	}()

	var n int

	query := "SELECT count(*) FROM pg_namespace WHERE nspname = ANY($1)"
	if err = conn.QueryRow(t.Context(), query, schemas).Scan(&n); err != nil {
		t.Fatalf("count schemas: %v", err)
	}

	return n
}

// errMigrate is the failure of the migrate function of the test below.
var errMigrate = errors.New("the migration fails")

// TestBuildTemplateDropsTheTemplateAfterAFailedMigrate checks that a build
// that fails after CREATE DATABASE does not leave the template database
// behind.
func TestBuildTemplateDropsTheTemplateAfterAFailedMigrate(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("%s is not set", envURL)
	}

	suffix := make([]byte, 6)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatalf("random name: %v", err)
	}

	name := "dustpan_test_pgtest" + hex.EncodeToString(suffix)

	// The build drops the database when it fails. This cleanup drops it when
	// the build keeps it, so a failed run leaves nothing.
	t.Cleanup(func() { dropDatabase(t, base, name) })

	migrate := func(context.Context, string) error { return errMigrate }

	if err := pgtest.BuildTemplate(t.Context(), base, name, migrate); err == nil {
		t.Fatal("BuildTemplate with a failed migrate = nil, want an error")
	}

	if databaseExists(t, base, name) {
		t.Fatalf("the template database %s stays after a failed build", name)
	}
}

// databaseExists reports whether the database name exists on the server of
// base.
func databaseExists(t *testing.T, base, name string) bool {
	t.Helper()

	conn, err := pgx.Connect(t.Context(), base)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(t.Context()); closeErr != nil {
			t.Errorf("close: %v", closeErr)
		}
	}()

	var n int

	query := "SELECT count(*) FROM pg_database WHERE datname = $1"
	if err = conn.QueryRow(t.Context(), query, name).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}

	return n > 0
}

// dropDatabase drops the database name on the server of base. A failure is
// only logged: a cleanup must not fail the test that it follows.
func dropDatabase(t *testing.T, base, name string) {
	t.Helper()

	conn, err := pgx.Connect(context.Background(), base)
	if err != nil {
		t.Logf("connect to drop %s: %v", name, err)

		return
	}

	defer func() {
		if closeErr := conn.Close(context.Background()); closeErr != nil {
			t.Logf("close: %v", closeErr)
		}
	}()

	drop := "DROP DATABASE IF EXISTS " + pgx.Identifier{name}.Sanitize() + " WITH (FORCE)"
	if _, execErr := conn.Exec(context.Background(), drop); execErr != nil {
		t.Logf("drop the database %s: %v", name, execErr)
	}
}

// TestCloneNameRefusesANameOverTheLimit checks the byte limit of a clone
// name: PostgreSQL keeps 63 bytes, so a longer name must not reach CREATE
// DATABASE.
func TestCloneNameRefusesANameOverTheLimit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		template string
		want     int
		wantErr  bool
	}{
		{"a name of 63 bytes", strings.Repeat("a", 49), 63, false},
		{"a name of 64 bytes", strings.Repeat("a", 50), 64, true},
		{"a multibyte name of 63 bytes", strings.Repeat("é", 24) + "a", 63, false},
		{"a multibyte name of 64 bytes", strings.Repeat("é", 25), 64, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := pgtest.CloneName(tt.template)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("CloneName of a %d-byte template = %q, want an error", len(tt.template), got)
				}

				return
			}

			if err != nil {
				t.Fatalf("CloneName = %v", err)
			}

			if len(got) != tt.want {
				t.Fatalf("clone name length = %d, want %d", len(got), tt.want)
			}
		})
	}
}

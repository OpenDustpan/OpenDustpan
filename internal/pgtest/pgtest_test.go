package pgtest_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
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

	name := "dustpan_test_pgtest_" + hex.EncodeToString(suffix)

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

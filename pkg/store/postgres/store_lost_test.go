package postgres_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/internal/api/problem"
	"opendustpan.org/opendustpan/pkg/installation"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/postgres"
)

// killWait bounds the wait for the server to end the sessions that a test
// terminated.
const killWait = 10 * time.Second

// withApplicationName returns rawURL with the application name set, so a test
// can end the sessions of one store and no other.
func withApplicationName(tb testing.TB, rawURL, name string) string {
	tb.Helper()

	u, err := url.Parse(rawURL)
	if err != nil {
		tb.Fatalf("parse the database URL: %v", err)
	}

	query := u.Query()
	query.Set("application_name", name)
	u.RawQuery = query.Encode()

	return u.String()
}

// terminateSessions ends every session of the application name from a second
// connection, as a failover or a restart of the server does, and waits until
// the server lists none.
func terminateSessions(ctx context.Context, tb testing.TB, base, name string) {
	tb.Helper()

	conn, err := pgx.Connect(ctx, base)
	if err != nil {
		tb.Fatalf("connect to end the sessions: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(ctx); closeErr != nil {
			tb.Errorf("close: %v", closeErr)
		}
	}()

	terminate := `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE application_name = $1`
	if _, execErr := conn.Exec(ctx, terminate, name); execErr != nil {
		tb.Fatalf("terminate the sessions: %v", execErr)
	}

	deadline := time.Now().Add(killWait)

	for {
		var left int

		count := `SELECT count(*) FROM pg_stat_activity WHERE application_name = $1`
		if scanErr := conn.QueryRow(ctx, count, name).Scan(&left); scanErr != nil {
			tb.Fatalf("count the sessions: %v", scanErr)
		}

		if left == 0 {
			return
		}

		if time.Now().After(deadline) {
			tb.Fatalf("%d sessions of %s still run after %s", left, name, killWait)
		}

		select {
		case <-ctx.Done():
			tb.Fatalf("wait for the sessions to end: %v", ctx.Err())
		case <-time.After(20 * time.Millisecond):
		}
	}
}

// storeLostCase is a call of the store and the sentinel of its port.
type storeLostCase struct {
	name string
	// port is the failure sentinel of the port of the call, or nil when the
	// port has none.
	port error
	call func(ctx context.Context, s *postgres.Store) error
}

// storeLostCases returns the calls of TestStoreLostWhenSessionsEnd: a read, a
// write, and one call of each port that has its own failure sentinel.
func storeLostCases() []storeLostCase {
	return []storeLostCase{
		{"read", nil, func(ctx context.Context, s *postgres.Store) error {
			_, err := s.GetNamespace(ctx, "acme")
			if errors.Is(err, namespace.ErrNotFound) {
				return nil
			}

			return err
		}},
		{"write", nil, func(ctx context.Context, s *postgres.Store) error {
			return s.CreateNamespace(ctx, namespace.Namespace{
				CreatedAt: time.Time{}, ID: "", Name: "lost-write", PrimaryInstallation: "",
				Overrides: namespace.Overrides{Forced: false, Unlocked: false}, FailoverVersion: 0,
			})
		}},
		{"maintenance state", maintenance.ErrUnavailable, func(ctx context.Context, s *postgres.Store) error {
			_, err := s.GetState(ctx)

			return err
		}},
		{"maintenance enter", maintenance.ErrUnavailable, func(ctx context.Context, s *postgres.Store) error {
			return s.SetState(ctx, maintenance.StateMaintenance)
		}},
		{"installation kind", installation.ErrUnavailable, func(ctx context.Context, s *postgres.Store) error {
			_, err := s.ReadKind(ctx)

			return err
		}},
		{"instances", installation.ErrUnavailable, func(ctx context.Context, s *postgres.Store) error {
			_, err := s.ListInstances(ctx)

			return err
		}},
	}
}

// TestStoreLostWhenSessionsEnd ends the sessions of a store with
// pg_terminate_backend from a second connection, the way a failover does, and
// proves that each call of the store returns an error that wraps
// namespace.ErrUnavailable, and also the sentinel of its port when the port has
// one. The calls use the real driver, so the test fails when the error
// translation of the store does not run.
func TestStoreLostWhenSessionsEnd(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL suite", envURL)
	}

	suffix := make([]byte, 6)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatalf("random name: %v", err)
	}

	for _, tc := range storeLostCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			app := "odp_lost_" + hex.EncodeToString(suffix) + "_" + hex.EncodeToString([]byte(tc.name))
			s := openURL(t, withApplicationName(t, isolatedURL(t, base), app))

			// The call works while the sessions live, and it leaves idle
			// sessions in the pool for the kill to end.
			if err := s.Ping(t.Context()); err != nil {
				t.Fatalf("ping before the kill: %v", err)
			}

			terminateSessions(t.Context(), t, base, app)

			err := tc.call(t.Context(), s)
			if !errors.Is(err, namespace.ErrUnavailable) {
				t.Fatalf(
					"%s after the sessions ended: err = %v, want it to wrap namespace.ErrUnavailable",
					tc.name,
					err,
				)
			}

			if tc.port != nil && !errors.Is(err, tc.port) {
				t.Fatalf("%s after the sessions ended: err = %v, want it to wrap %v too", tc.name, err, tc.port)
			}

			checkAPIAnswers503(t, err)
		})
	}
}

// checkAPIAnswers503 checks that the API answers err 503 with Retry-After,
// not 500.
func checkAPIAnswers503(t *testing.T, err error) {
	t.Helper()

	rec := httptest.NewRecorder()
	problem.Writer{Logger: slog.New(slog.DiscardHandler), KeepDenied: nil, Classify: nil}.Write(t.Context(), rec, err)

	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") != "5" {
		t.Fatalf("the API answers %d with Retry-After %q, want 503 and 5", rec.Code, rec.Header().Get("Retry-After"))
	}
}

// makeDatabaseReadOnly makes every new session of the database that
// schemaURL names read-only, as a primary that a failover demoted: the server
// answers SQLSTATE 25006 to each write on a new session. A session that
// exists keeps its setting. The isolated URL names a database of its own, so
// the change reaches no other test.
func makeDatabaseReadOnly(tb testing.TB, schemaURL string) {
	tb.Helper()

	conn, err := pgx.Connect(tb.Context(), schemaURL)
	if err != nil {
		tb.Fatalf("connect to make the database read-only: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(tb.Context()); closeErr != nil {
			tb.Errorf("close: %v", closeErr)
		}
	}()

	const alter = `DO $$ BEGIN EXECUTE format('ALTER DATABASE %I SET default_transaction_read_only = on', ` +
		`current_database()); END $$`
	if _, execErr := conn.Exec(tb.Context(), alter); execErr != nil {
		tb.Fatalf("make the database read-only: %v", execErr)
	}
}

// TestStoreLostWhenDatabaseIsReadOnly proves that a write on a read-only
// session (SQLSTATE 25006, as on a demoted primary) returns an error that
// wraps namespace.ErrUnavailable, and that the API answers 503 with
// Retry-After. The read on the same session still works. The store first
// holds sessions that were writable, and the test ends them, so the pool
// opens new sessions that are read-only, as a pool does after a failover.
func TestStoreLostWhenDatabaseIsReadOnly(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL suite", envURL)
	}

	suffix := make([]byte, 6)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatalf("random name: %v", err)
	}

	app := "odp_readonly_" + hex.EncodeToString(suffix)
	schemaURL := isolatedURL(t, base)
	s := openURL(t, withApplicationName(t, schemaURL, app))

	if err := s.Ping(t.Context()); err != nil {
		t.Fatalf("ping before the change: %v", err)
	}

	makeDatabaseReadOnly(t, schemaURL)
	terminateSessions(t.Context(), t, base, app)

	// A read that meets a pool connection which the kill ended fails once
	// and drops that connection. The next read opens a read-only session.
	var readErr error

	for range 3 {
		if _, readErr = s.GetNamespace(t.Context(), "acme"); !errors.Is(readErr, namespace.ErrUnavailable) {
			break
		}
	}

	if !errors.Is(readErr, namespace.ErrNotFound) {
		t.Fatalf("read on a read-only session: err = %v, want namespace.ErrNotFound", readErr)
	}

	err := s.CreateNamespace(t.Context(), namespace.Namespace{
		CreatedAt: time.Now(), ID: namespace.NewID(), Name: "read-only-write", PrimaryInstallation: "",
		Overrides: namespace.Overrides{Forced: false, Unlocked: false}, FailoverVersion: 0,
	})
	if err == nil || !errors.Is(err, namespace.ErrUnavailable) {
		t.Fatalf("write on a read-only session: err = %v, want it to wrap namespace.ErrUnavailable", err)
	}

	checkAPIAnswers503(t, err)
}

// TestOpenRefusesReadOnlySessionWithReadWriteAttribute proves that the store
// passes target_session_attrs=read-write to the driver: a pool refuses a
// session that is read-only, so it never holds a connection to a demoted
// primary. The same URL opens while the database is writable.
func TestOpenRefusesReadOnlySessionWithReadWriteAttribute(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL suite", envURL)
	}

	schemaURL := isolatedURL(t, base)

	u, err := url.Parse(schemaURL)
	if err != nil {
		t.Fatalf("parse the database URL: %v", err)
	}

	query := u.Query()
	query.Set("target_session_attrs", "read-write")
	u.RawQuery = query.Encode()
	readWriteURL := u.String()

	openURL(t, readWriteURL)

	makeDatabaseReadOnly(t, schemaURL)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	if s, openErr := postgres.Open(ctx, readWriteURL); openErr == nil {
		s.Close()

		t.Fatal("Open on a read-only database with target_session_attrs=read-write answered no error")
	}
}

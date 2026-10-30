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

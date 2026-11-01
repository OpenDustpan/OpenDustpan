package postgres

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/internal/storeerr"
)

// releaseWait bounds the wait for the release of a connection, which the
// pool runs in its own goroutine.
const releaseWait = 5 * time.Second

// longTestStore returns a store over one connection at dbURL, with the
// release hooks of the long paths and the given runtime parameters.
func longTestStore(t *testing.T, dbURL string, params map[string]string) *Store {
	t.Helper()

	cfg, _, err := poolConfig(dbURL)
	if err != nil {
		t.Fatalf("poolConfig: %v", err)
	}

	maps.Copy(cfg.ConnConfig.RuntimeParams, params)

	s := new(Store)
	s.marks = newLongMarks()
	s.logger = slog.New(slog.DiscardHandler)
	cfg.MaxConns = 1
	cfg.AfterRelease = s.marks.afterRelease
	cfg.BeforeClose = s.marks.beforeClose

	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatalf("open the pool: %v", err)
	}

	t.Cleanup(pool.Close)

	s.pool = pool

	return s
}

// show reads the setting name on one connection of the pool of s. The pool
// has one connection, so it is the connection that the test released.
func show(t *testing.T, s *Store, name string) string {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), releaseWait)
	defer cancel()

	var value string

	if err := s.pool.QueryRow(ctx, "SELECT current_setting($1)", name).Scan(&value); err != nil {
		t.Fatalf("read %s: %v", name, err)
	}

	return value
}

// TestInLongTxSetsBothBounds proves that a long path runs with both server
// bounds of its kind inside its transaction, and that the connection has the
// start-up values again after the release: the server timeouts and the search
// path with the control schema.
func TestInLongTxSetsBothBounds(t *testing.T) {
	t.Parallel()

	s := longTestStore(t, testURL(t), nil)
	bound := longBound{name: "test", statement: 7 * time.Minute, idle: 3 * time.Minute}

	err := s.inLongTx(t.Context(), bound, defaultTx(), func(tx pgx.Tx) error {
		var statement, idle string

		row := tx.QueryRow(t.Context(),
			"SELECT current_setting('statement_timeout'), current_setting('idle_in_transaction_session_timeout')")
		if scanErr := row.Scan(&statement, &idle); scanErr != nil {
			return fmt.Errorf("read the bounds: %w", scanErr)
		}

		if statement != "7min" || idle != "3min" {
			return fmt.Errorf("bounds %s and %s: %w", statement, idle, errUnexpectedBound)
		}

		return nil
	})
	if err != nil {
		t.Fatalf("inLongTx: %v", err)
	}

	if got := show(t, s, "statement_timeout"); got != "25s" {
		t.Errorf("statement_timeout after the release = %q, want 25s", got)
	}

	if got := show(t, s, "idle_in_transaction_session_timeout"); got != "1min" {
		t.Errorf("idle_in_transaction_session_timeout after the release = %q, want 1min", got)
	}

	if got := show(t, s, "client_connection_check_interval"); got != "5s" {
		t.Errorf("client_connection_check_interval = %q, want 5s", got)
	}

	if got := show(t, s, "search_path"); !strings.Contains(got, "_control") {
		t.Errorf("search_path after the release = %q, want the control schema", got)
	}
}

// errUnexpectedBound reports a bound that the transaction did not get.
var errUnexpectedBound = errors.New("unexpected bound")

// TestReleaseResetsMarkedConnection proves the second layer: a session value
// that a long path set stays off the next request, because the release of a
// marked connection runs RESET ALL. The release of an unmarked connection
// runs no statement, so the API paths pay no round trip for it.
func TestReleaseResetsMarkedConnection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		marked bool
		want   string
	}{
		{"long-path connection", true, "25s"},
		{"connection of an API path", false, "0"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := longTestStore(t, testURL(t), nil)

			conn, err := s.pool.Acquire(t.Context())
			if err != nil {
				t.Fatalf("acquire: %v", err)
			}

			if tc.marked {
				s.marks.mark(conn.Conn())
			}

			if _, execErr := conn.Exec(t.Context(), "SET statement_timeout = 0"); execErr != nil {
				t.Fatalf("set: %v", execErr)
			}

			conn.Release()

			if got := show(t, s, "statement_timeout"); got != tc.want {
				t.Fatalf("statement_timeout after the release = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestLongMarksDoNotGrow proves that the mark of a long-path connection goes
// away at its release, before its hijack and at its destroy.
func TestLongMarksDoNotGrow(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		end  func(ctx context.Context, s *Store, conn *pgxpool.Conn) error
	}{
		{"release", func(_ context.Context, _ *Store, conn *pgxpool.Conn) error {
			conn.Release()

			return nil
		}},
		{"hijack", func(ctx context.Context, s *Store, conn *pgxpool.Conn) error {
			s.unmarkForHijack(conn)

			if err := conn.Hijack().Close(ctx); err != nil {
				return fmt.Errorf("close: %w", err)
			}

			return nil
		}},
		{"destroy", func(ctx context.Context, _ *Store, conn *pgxpool.Conn) error {
			// A connection in a transaction does not go back to the pool:
			// the pool destroys it, and BeforeClose drops the mark.
			if _, err := conn.Exec(ctx, "BEGIN"); err != nil {
				return fmt.Errorf("begin: %w", err)
			}

			conn.Release()

			return nil
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := longTestStore(t, testURL(t), nil)

			conn, err := s.longConn(t.Context())
			if err != nil {
				t.Fatalf("longConn: %v", err)
			}

			if s.marks.size() != 1 {
				t.Fatalf("marks = %d after longConn, want 1", s.marks.size())
			}

			if endErr := tc.end(t.Context(), s, conn); endErr != nil {
				t.Fatalf("end: %v", endErr)
			}

			waitFor(t, func() bool { return s.marks.size() == 0 })
		})
	}
}

// waitFor waits up to releaseWait for done, and fails the test after that.
func waitFor(t *testing.T, done func() bool) {
	t.Helper()

	deadline := time.NewTimer(releaseWait)
	defer deadline.Stop()

	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()

	for !done() {
		select {
		case <-deadline.C:
			t.Fatalf("the condition did not hold within %s", releaseWait)
		case <-t.Context().Done():
			t.Fatal("the test ended before the condition held")
		case <-tick.C:
		}
	}
}

// TestReleaseOfStalledLongConnFreesTheSlot proves the deadline of the reset:
// when the connection of a long path stops answering and does not close, the
// RESET ALL of its release ends within resetTimeout, and the pool destroys
// the connection, so its slot does not stay taken until the TCP timeout of
// the host. pgx closes a broken connection after a cancel request on a new
// connection. A dial to a host that stopped answering fails, which the
// proxy copies, so the close adds little to the reset.
func TestReleaseOfStalledLongConnFreesTheSlot(t *testing.T) {
	t.Parallel()

	proxy, proxied := startHoldProxy(t, testURL(t))
	s := longTestStore(t, proxied, nil)

	conn, err := s.longConn(t.Context())
	if err != nil {
		t.Fatalf("longConn: %v", err)
	}

	proxy.hold()

	start := time.Now()

	conn.Release()
	waitFor(t, func() bool { return s.pool.Stat().TotalConns() == 0 })

	if elapsed := time.Since(start); elapsed > resetTimeout+time.Second {
		t.Errorf("the slot was free after %s, want at most %s", elapsed, resetTimeout+time.Second)
	}
}

// TestServerStatementTimeout proves that a statement longer than the server
// timeout ends on the server with SQLSTATE 57014, that the error is the class
// StatementTimeout and wraps ErrUnavailable but not ErrUnreachable, and that
// the connection stays in the pool and answers the next statement. The test
// uses a short timeout to run fast.
func TestServerStatementTimeout(t *testing.T) {
	t.Parallel()

	s := longTestStore(t, testURL(t), map[string]string{"statement_timeout": "200"})
	g := guardedDB{inner: s.pool, rt: roundTrip{deadline: time.Minute, watch: nil, readOnlyTx: false}}

	_, err := g.Exec(t.Context(), "SELECT pg_sleep(5)")

	var unavailable *storeerr.Error
	if !errors.As(err, &unavailable) || unavailable.Class() != storeerr.StatementTimeout {
		t.Fatalf("err = %v, want the class %q", err, storeerr.StatementTimeout)
	}

	if !errors.Is(err, namespace.ErrUnavailable) || errors.Is(err, namespace.ErrUnreachable) {
		t.Fatalf("err = %v, want ErrUnavailable and not ErrUnreachable", err)
	}

	if _, execErr := g.Exec(t.Context(), "SELECT 1"); execErr != nil {
		t.Fatalf("next statement: %v", execErr)
	}

	if got := s.pool.Stat().NewConnsCount(); got != 1 {
		t.Errorf("the pool opened %d connections, want 1: the connection must stay", got)
	}
}

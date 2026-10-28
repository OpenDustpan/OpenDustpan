package sqlite

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"modernc.org/sqlite"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/internal/storeerr"
)

// openShortBusy opens path through the guarded driver with a 1 ms busy bound.
func openShortBusy(t *testing.T, path string) *sql.DB {
	t.Helper()

	conn, err := openGuarded(fmt.Sprintf("file:%s?_pragma=busy_timeout(1)&_txlock=immediate", path))
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := conn.Close(); closeErr != nil {
			t.Logf("close: %v", closeErr)
		}
	})

	return conn
}

// busyCase is one statement and what translate must make of its error.
type busyCase struct {
	name        string
	run         func() error
	unavailable bool
}

// busyCases returns the table of TestTranslate: statements on waiter while
// hold keeps the write lock.
func busyCases(t *testing.T, waiter *sql.DB, hold *sql.Tx) []busyCase {
	t.Helper()

	wrap := func(what string, err error) error {
		if err != nil {
			return fmt.Errorf("%s: %w", what, err)
		}

		return nil
	}

	return []busyCase{
		{"begin while another writer holds the lock", func() error {
			tx, err := waiter.BeginTx(t.Context(), nil)
			if err == nil {
				err = tx.Rollback()
			}

			return wrap("begin", err)
		}, true},
		{"exec while another writer holds the lock", func() error {
			_, err := waiter.ExecContext(t.Context(), `INSERT INTO t (id) VALUES (1)`)

			return wrap("insert", err)
		}, true},
		{"constraint error passes through", func() error {
			_, err := hold.ExecContext(t.Context(), `INSERT INTO t (id) VALUES (2)`)
			if err != nil {
				return wrap("first insert", err)
			}

			_, err = hold.ExecContext(t.Context(), `INSERT INTO t (id) VALUES (2)`)

			return wrap("second insert", err)
		}, false},
		{"syntax error passes through", func() error {
			_, err := hold.ExecContext(t.Context(), `NOT SQL`)

			return wrap("syntax", err)
		}, false},
	}
}

func TestTranslate(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "busy.db")
	holder := openShortBusy(t, path)
	waiter := openShortBusy(t, path)

	if _, err := holder.ExecContext(t.Context(), `CREATE TABLE t (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatalf("create table: %v", err)
	}

	hold, err := holder.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatalf("hold the write lock: %v", err)
	}

	t.Cleanup(func() {
		if rollbackErr := hold.Rollback(); rollbackErr != nil {
			t.Logf("rollback: %v", rollbackErr)
		}
	})

	for _, tc := range busyCases(t, waiter, hold) {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			checkBusy(t, tc)
		})
	}
}

// checkBusy checks the error of one statement.
func checkBusy(t *testing.T, tc busyCase) {
	t.Helper()

	err := tc.run()
	if err == nil {
		t.Fatal("got no error")
	}

	var got *storeerr.Error

	translated := errors.As(err, &got)

	var driverErr *sqlite.Error

	kept := errors.As(err, &driverErr)

	switch {
	case tc.unavailable && (!translated || !errors.Is(err, namespace.ErrUnavailable) || got.Class() != storeerr.Busy):
		t.Fatalf("err = %v, want ErrUnavailable of class busy", err)
	case tc.unavailable && !kept:
		t.Fatalf("err = %v lost the driver error", err)
	case !tc.unavailable && (translated || errors.Is(err, namespace.ErrUnavailable)):
		t.Fatalf("err = %v, want a pass-through", err)
	case !tc.unavailable && !kept:
		t.Fatalf("err = %v is not a driver error", err)
	default:
	}
}

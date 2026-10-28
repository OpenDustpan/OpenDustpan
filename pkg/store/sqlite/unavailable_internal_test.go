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

	t.Cleanup(func() { _ = conn.Close() })

	return conn
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

	t.Cleanup(func() { _ = hold.Rollback() })

	tests := []struct {
		name        string
		run         func() error
		unavailable bool
	}{
		{"begin while another writer holds the lock", func() error {
			tx, err := waiter.BeginTx(t.Context(), nil)
			if err == nil {
				_ = tx.Rollback()
			}

			return err
		}, true},
		{"exec while another writer holds the lock", func() error {
			_, err := waiter.ExecContext(t.Context(), `INSERT INTO t (id) VALUES (1)`)

			return err
		}, true},
		{"constraint error passes through", func() error {
			_, err := hold.ExecContext(t.Context(), `INSERT INTO t (id) VALUES (1)`)
			if err != nil {
				return err
			}

			_, err = hold.ExecContext(t.Context(), `INSERT INTO t (id) VALUES (1)`)

			return err
		}, false},
		{"syntax error passes through", func() error {
			_, err := hold.ExecContext(t.Context(), `NOT SQL`)

			return err
		}, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.run()
			if err == nil {
				t.Fatal("got no error")
			}

			got, ok := errors.AsType[*storeerr.Error](err)
			if tc.unavailable {
				if !ok || !errors.Is(err, namespace.ErrUnavailable) || got.Class() != storeerr.Busy {
					t.Fatalf("err = %v, want ErrUnavailable of class busy", err)
				}

				if _, kept := errors.AsType[*sqlite.Error](err); !kept {
					t.Fatalf("err = %v lost the driver error", err)
				}

				return
			}

			if ok || errors.Is(err, namespace.ErrUnavailable) {
				t.Fatalf("err = %v, want a pass-through", err)
			}

			if _, kept := errors.AsType[*sqlite.Error](err); !kept {
				t.Fatalf("err = %v is not a driver error", err)
			}
		})
	}
}

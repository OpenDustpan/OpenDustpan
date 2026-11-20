package sqlite

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"testing"

	"opendustpan.org/opendustpan/internal/store/sqlite/internal/db"
)

// The static errors of the fake read driver and of the reads it serves.
var (
	errFakeStatement = errors.New("the fake driver serves no statement")
	errFakeRollback  = errors.New("the fake read transaction rollback failed")
	errFakeSelect    = errors.New("select labels of deleted unit failed")
)

// fakeReadConn is a driver connection for the read-transaction tests. Its
// rollback returns rollbackErr and reports that the read transaction reached
// Rollback. The SQLite driver does not fail the rollback of a read-only
// transaction on demand, so the tests use this connection to reach the
// rollback branch of inDeletedFactsReadTx.
type fakeReadConn struct {
	rollbackErr    error
	rollbackCalled bool
}

func (*fakeReadConn) Prepare(string) (driver.Stmt, error) { return nil, errFakeStatement }
func (*fakeReadConn) Close() error                        { return nil }
func (c *fakeReadConn) Begin() (driver.Tx, error)         { return c, nil }

func (c *fakeReadConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	return c, nil
}

func (*fakeReadConn) Commit() error { return nil }

func (c *fakeReadConn) Rollback() error {
	c.rollbackCalled = true

	return c.rollbackErr
}

// fakeReadConnector hands the one fakeReadConn to database/sql.
type fakeReadConnector struct{ conn driver.Conn }

func (c fakeReadConnector) Connect(context.Context) (driver.Conn, error) { return c.conn, nil }
func (fakeReadConnector) Driver() driver.Driver                          { return fakeReadDriver{} }

// fakeReadDriver serves no connection of its own; the connector hands out the
// one test connection.
type fakeReadDriver struct{}

func (fakeReadDriver) Open(string) (driver.Conn, error) { return nil, errFakeStatement }

// fakeReadStore returns a store on a fake read connection whose rollback
// returns rollbackErr, and the connection itself.
func fakeReadStore(t *testing.T, rollbackErr error) (*Store, *fakeReadConn) {
	t.Helper()

	conn := &fakeReadConn{rollbackErr: rollbackErr, rollbackCalled: false}
	sqlDB := sql.OpenDB(fakeReadConnector{conn: conn})

	t.Cleanup(func() {
		if closeErr := sqlDB.Close(); closeErr != nil {
			t.Errorf("close the fake database: %v", closeErr)
		}
	})

	store := new(Store)
	store.conn = sqlDB
	store.queries = db.New(sqlDB)

	return store, conn
}

// TestDeletedUnitFactsSelectAndRollbackFailure reaches the snapshot read
// helper of the deleted-unit facts when the select and the rollback both
// fail: the call returns the select error alone, keeps no rollback error
// beside it, and closes the transaction.
func TestDeletedUnitFactsSelectAndRollbackFailure(t *testing.T) {
	t.Parallel()

	store, conn := fakeReadStore(t, errFakeRollback)

	err := store.inDeletedFactsReadTx(t.Context(), func(*db.Queries) error { return errFakeSelect })

	if !errors.Is(err, errFakeSelect) {
		t.Fatalf("inDeletedFactsReadTx() = %v, want the select error", err)
	}

	if errors.Is(err, errFakeRollback) {
		t.Fatalf("inDeletedFactsReadTx() = %v, want no rollback error beside the select error", err)
	}

	if !conn.rollbackCalled {
		t.Fatal("inDeletedFactsReadTx() left the read transaction open, want a rollback")
	}
}

// TestInDeletedFactsReadTxRollbackThenClose proves the close of the read after
// a failed read. The close rolls the transaction back and keeps the error of
// the read: the call returns the select error, not the error of the close.
func TestInDeletedFactsReadTxRollbackThenClose(t *testing.T) {
	t.Parallel()

	store, conn := fakeReadStore(t, nil)

	err := store.inDeletedFactsReadTx(t.Context(), func(*db.Queries) error { return errFakeSelect })

	if !errors.Is(err, errFakeSelect) {
		t.Fatalf("inDeletedFactsReadTx() = %v, want the select error", err)
	}

	if !conn.rollbackCalled {
		t.Fatal("inDeletedFactsReadTx() left the read transaction open, want a rollback")
	}
}

// TestInDeletedFactsReadTxReportsARollbackFailureOfItsOwn proves the other
// side of the close: when the read succeeds and the rollback fails, the call
// returns the rollback error, because no read error hides it.
func TestInDeletedFactsReadTxReportsARollbackFailureOfItsOwn(t *testing.T) {
	t.Parallel()

	store, conn := fakeReadStore(t, errFakeRollback)

	err := store.inDeletedFactsReadTx(t.Context(), func(*db.Queries) error { return nil })

	if !errors.Is(err, errFakeRollback) {
		t.Fatalf("inDeletedFactsReadTx() = %v, want the rollback error", err)
	}

	if !conn.rollbackCalled {
		t.Fatal("inDeletedFactsReadTx() left the read transaction open, want a rollback")
	}
}

// TestInDeletedFactsReadTxClosesOnPanic proves the deferred close of the read
// transaction: a panic in fn must not leave the transaction open.
func TestInDeletedFactsReadTxClosesOnPanic(t *testing.T) {
	t.Parallel()

	store, conn := fakeReadStore(t, errFakeRollback)

	defer func() {
		if recover() == nil {
			t.Error("inDeletedFactsReadTx() did not panic, want the panic of fn")

			return
		}

		if !conn.rollbackCalled {
			t.Error("inDeletedFactsReadTx() left the read transaction open after the panic")
		}
	}()

	err := store.inDeletedFactsReadTx(t.Context(), func(*db.Queries) error { panic("the read panicked") })
	if err != nil {
		t.Errorf("inDeletedFactsReadTx() = %v, want the panic of fn", err)
	}
}

package sqlite

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"opendustpan.org/opendustpan/pkg/store/internal/storeerr"
)

// primaryCodeMask keeps the primary result code of an extended result code.
const primaryCodeMask = 0xff

// translate returns a storeerr.Error for a busy or locked database, and err
// for any other error. The busy timeout of the connection is the busy bound,
// so an error of this class comes after the whole bound. It is the one place
// where this adapter makes that call: the database/sql driver below calls it
// for every error that the driver returns.
func translate(err error) error {
	if err == nil || storeerr.Is(err) {
		return err
	}

	sqliteErr, ok := errors.AsType[*sqlite.Error](err)
	if !ok {
		return err
	}

	switch sqliteErr.Code() & primaryCodeMask {
	case sqlite3.SQLITE_BUSY, sqlite3.SQLITE_LOCKED:
		return storeerr.New(storeerr.Busy, err)
	default:
		return err
	}
}

// openGuarded opens dsn with a driver that translates its errors.
func openGuarded(dsn string) (*sql.DB, error) {
	probe, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite probe: %w", err)
	}

	inner := probe.Driver()

	if err := probe.Close(); err != nil {
		return nil, fmt.Errorf("close sqlite probe: %w", err)
	}

	return sql.OpenDB(guardedConnector{inner: inner, dsn: dsn}), nil
}

type guardedConnector struct {
	inner driver.Driver
	dsn   string
}

func (c guardedConnector) Connect(context.Context) (driver.Conn, error) {
	conn, err := c.inner.Open(c.dsn)
	if err != nil {
		return nil, translate(err)
	}

	inner, ok := conn.(innerConn)
	if !ok {
		return nil, errors.Join(errDriverShape, conn.Close())
	}

	return &guardedConn{inner: inner}, nil
}

func (c guardedConnector) Driver() driver.Driver { return c.inner }

// innerConn is what the SQLite driver connection implements. The wrapper
// forwards each method, so it needs all of them.
type innerConn interface {
	driver.Conn
	driver.ConnBeginTx
	driver.ConnPrepareContext
	driver.ExecerContext
	driver.QueryerContext
	driver.Pinger
	driver.SessionResetter
	driver.Validator
}

// innerStmt is what the SQLite driver statement implements.
type innerStmt interface {
	driver.Stmt
	driver.StmtExecContext
	driver.StmtQueryContext
}

// errDriverShape reports a SQLite driver that lacks an interface that the
// wrapper forwards.
var errDriverShape = errors.New("the sqlite driver does not implement the forwarded interfaces")

type guardedConn struct{ inner innerConn }

func (c *guardedConn) Prepare(query string) (driver.Stmt, error) {
	return c.PrepareContext(context.Background(), query)
}

func (c *guardedConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	st, err := c.inner.PrepareContext(ctx, query)
	if err != nil {
		return nil, translate(err)
	}

	inner, ok := st.(innerStmt)
	if !ok {
		return nil, errDriverShape
	}

	return &guardedStmt{inner: inner}, nil
}

func (c *guardedConn) Close() error { return translate(c.inner.Close()) }

func (c *guardedConn) Begin() (driver.Tx, error) {
	return c.BeginTx(context.Background(), driver.TxOptions{Isolation: driver.IsolationLevel(sql.LevelDefault), ReadOnly: false})
}

func (c *guardedConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	tx, err := c.inner.BeginTx(ctx, opts)
	if err != nil {
		return nil, translate(err)
	}

	return guardedTx{inner: tx}, nil
}

func (c *guardedConn) ExecContext(
	ctx context.Context, query string, args []driver.NamedValue,
) (driver.Result, error) {
	res, err := c.inner.ExecContext(ctx, query, args)

	return res, translate(err)
}

func (c *guardedConn) QueryContext(
	ctx context.Context, query string, args []driver.NamedValue,
) (driver.Rows, error) {
	rows, err := c.inner.QueryContext(ctx, query, args)
	if err != nil {
		return nil, translate(err)
	}

	return &guardedRows{Rows: rows}, nil
}

func (c *guardedConn) Ping(ctx context.Context) error { return translate(c.inner.Ping(ctx)) }

func (c *guardedConn) ResetSession(ctx context.Context) error {
	return translate(c.inner.ResetSession(ctx))
}

func (c *guardedConn) IsValid() bool { return c.inner.IsValid() }

type guardedTx struct{ inner driver.Tx }

func (t guardedTx) Commit() error   { return translate(t.inner.Commit()) }
func (t guardedTx) Rollback() error { return translate(t.inner.Rollback()) }

type guardedStmt struct{ inner innerStmt }

func (s *guardedStmt) Close() error  { return translate(s.inner.Close()) }
func (s *guardedStmt) NumInput() int { return s.inner.NumInput() }

func (s *guardedStmt) Exec(args []driver.Value) (driver.Result, error) {
	return s.ExecContext(context.Background(), namedValues(args))
}

func (s *guardedStmt) Query(args []driver.Value) (driver.Rows, error) {
	return s.QueryContext(context.Background(), namedValues(args))
}

func (s *guardedStmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	res, err := s.inner.ExecContext(ctx, args)

	return res, translate(err)
}

func (s *guardedStmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	rows, err := s.inner.QueryContext(ctx, args)
	if err != nil {
		return nil, translate(err)
	}

	return &guardedRows{Rows: rows}, nil
}

// namedValues numbers positional arguments, as database/sql does.
func namedValues(args []driver.Value) []driver.NamedValue {
	out := make([]driver.NamedValue, len(args))
	for i, v := range args {
		out[i] = driver.NamedValue{Name: "", Ordinal: i + 1, Value: v}
	}

	return out
}

type guardedRows struct{ driver.Rows }

func (r *guardedRows) Next(dest []driver.Value) error {
	err := r.Rows.Next(dest)
	if errors.Is(err, io.EOF) {
		return err
	}

	return translate(err)
}

func (r *guardedRows) Close() error { return translate(r.Rows.Close()) }

package postgres

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"syscall"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"opendustpan.org/opendustpan/pkg/store/internal/storeerr"
)

// SQLSTATE codes that mean the database does not answer, besides the class 08
// of connection exceptions.
const (
	connectionClass  = "08"
	adminShutdown    = "57P01"
	crashShutdown    = "57P02"
	cannotConnectNow = "57P03"
	readOnlyTransact = "25006"
)

// classify reports whether err means that the database does not answer, and
// why. A canceled context is not a database failure.
func classify(err error) (storeerr.Class, bool) {
	if err == nil || errors.Is(err, context.Canceled) {
		return "", false
	}

	var connectErr *pgconn.ConnectError
	if errors.As(err, &connectErr) {
		return storeerr.ConnectionFailed, true
	}

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return classifyCode(pgErr.Code)
	}

	// A deadline of the caller is not a database failure.
	if errors.Is(err, context.DeadlineExceeded) {
		return "", false
	}

	var opErr *net.OpError

	if errors.As(err, &opErr) ||
		errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EPIPE) ||
		errors.Is(err, syscall.ECONNREFUSED) {
		return storeerr.ConnectionFailed, true
	}

	return "", false
}

// classifyCode maps a SQLSTATE code to a class.
func classifyCode(code string) (storeerr.Class, bool) {
	switch {
	case strings.HasPrefix(code, connectionClass):
		return storeerr.ConnectionFailed, true
	case code == adminShutdown, code == crashShutdown, code == cannotConnectNow:
		return storeerr.Shutdown, true
	case code == readOnlyTransact:
		return storeerr.ReadOnly, true
	default:
		return "", false
	}
}

// translate returns a storeerr.Error for an error that means the database
// does not answer, and err for any other error. It is the one place where
// this adapter makes that call.
func translate(err error) error {
	if err == nil || storeerr.Is(err) {
		return err
	}

	if class, ok := classify(err); ok {
		return storeerr.New(class, err)
	}

	return err
}

// guardedDB is the db.DBTX of the store pool. It translates the error of each
// statement, so no call site does.
type guardedDB struct {
	inner interface {
		Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
		Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
		QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	}
}

func (g guardedDB) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	tag, err := g.inner.Exec(ctx, sql, args...)

	return tag, translate(err)
}

func (g guardedDB) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	rows, err := g.inner.Query(ctx, sql, args...)
	if err != nil {
		return rows, translate(err)
	}

	return guardedRows{Rows: rows}, nil
}

func (g guardedDB) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return guardedRow{Row: g.inner.QueryRow(ctx, sql, args...)}
}

// guardedRows translates the error that ends a read.
type guardedRows struct{ pgx.Rows }

func (r guardedRows) Err() error { return translate(r.Rows.Err()) }

// guardedRow translates the error of a single-row read.
type guardedRow struct{ pgx.Row }

func (r guardedRow) Scan(dest ...any) error { return translate(r.Row.Scan(dest...)) }

// guardedPool starts the transactions of the store. Each transaction it
// starts translates its errors, so a call site that uses the transaction does
// not.
type guardedPool struct {
	pool interface {
		Begin(ctx context.Context) (pgx.Tx, error)
		BeginTx(ctx context.Context, opts pgx.TxOptions) (pgx.Tx, error)
	}
}

func (g guardedPool) Begin(ctx context.Context) (pgx.Tx, error) {
	tx, err := g.pool.Begin(ctx)
	if err != nil {
		return nil, translate(err)
	}

	return guardedTx{Tx: tx}, nil
}

func (g guardedPool) BeginTx(ctx context.Context, opts pgx.TxOptions) (pgx.Tx, error) {
	tx, err := g.pool.BeginTx(ctx, opts)
	if err != nil {
		return nil, translate(err)
	}

	return guardedTx{Tx: tx}, nil
}

// guardedTx translates the error of each statement of a transaction and of
// its commit.
type guardedTx struct{ pgx.Tx }

func (t guardedTx) Commit(ctx context.Context) error { return translate(t.Tx.Commit(ctx)) }

func (t guardedTx) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return guardedDB{inner: t.Tx}.Exec(ctx, sql, args...)
}

func (t guardedTx) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return guardedDB{inner: t.Tx}.Query(ctx, sql, args...)
}

func (t guardedTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return guardedDB{inner: t.Tx}.QueryRow(ctx, sql, args...)
}

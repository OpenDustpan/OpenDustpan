package postgres

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"opendustpan.org/opendustpan/pkg/store/internal/storeerr"
)

// SQLSTATE codes that mean the database does not answer. The connection
// exceptions are listed one by one: 08P01 (protocol violation) is a bug of
// the client or the server and not an outage, so it stays a server error.
// 53300 (too many connections) is a limit that clears when sessions end. The
// other codes of class 53 (for example 53200, out of memory, and 53100, disk
// full) stay server errors: the same call is likely to fail again, and a
// client that tries again would only hide the fault.
const (
	connectionDoesNotExist    = "08003"
	connectionFailure         = "08006"
	connectionException       = "08000"
	unableToEstablish         = "08001"
	rejectedEstablishment     = "08004"
	transactionResolutionLost = "08007"
	adminShutdown             = "57P01"
	crashShutdown             = "57P02"
	cannotConnectNow          = "57P03"
	readOnlyTransact          = "25006"
	tooManyConnections        = "53300"
	queryCanceled             = "57014"
)

// classify reports whether err means that the database does not answer, and
// why. A canceled context is not a database failure.
func classify(err error) (storeerr.Class, bool) {
	if err == nil || errors.Is(err, context.Canceled) {
		return "", false
	}

	if connectErr, ok := errors.AsType[*pgconn.ConnectError](err); ok && connectErr != nil {
		return storeerr.ConnectionFailed, true
	}

	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr != nil {
		return classifyCode(pgErr.Code)
	}

	// A deadline of the caller is not a database failure.
	if errors.Is(err, context.DeadlineExceeded) {
		return "", false
	}

	if connectionLost(err) {
		return storeerr.ConnectionFailed, true
	}

	return "", false
}

// connectionLost reports whether err is a failure of the network connection
// to the database.
func connectionLost(err error) bool {
	if opErr, ok := errors.AsType[*net.OpError](err); ok && opErr != nil {
		return true
	}

	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EPIPE) ||
		errors.Is(err, syscall.ECONNREFUSED)
}

// classifyCode maps a SQLSTATE code to a class.
func classifyCode(code string) (storeerr.Class, bool) {
	switch code {
	case connectionException, unableToEstablish, connectionDoesNotExist, rejectedEstablishment,
		connectionFailure, transactionResolutionLost:
		return storeerr.ConnectionFailed, true
	case adminShutdown, crashShutdown, cannotConnectNow:
		return storeerr.Shutdown, true
	case readOnlyTransact:
		return storeerr.ReadOnly, true
	case tooManyConnections:
		return storeerr.Capacity, true
	case queryCanceled:
		// The pool sends no cancel request, so a 57014 comes from the
		// statement timeout of the server or from an operator who canceled
		// the backend. Both clear when the client tries again.
		return storeerr.StatementTimeout, true
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

// statementDeadline bounds each round trip of the guarded wrappers, also when
// the caller gives no deadline. A connection that stops answering and does not
// close then ends within this time, not at the TCP timeout of the host.
const statementDeadline = 30 * time.Second

// errStatementDeadline is the cause of the context of each guarded round trip.
// It tells the statement deadline apart from a deadline of the caller.
var errStatementDeadline = errors.New("database statement deadline")

// roundTrip is what each guarded wrapper applies to a round trip: the
// statement deadline, and the watch of read-only errors.
type roundTrip struct {
	// deadline is the statement deadline. Zero means statementDeadline.
	// Tests set a shorter one.
	deadline time.Duration
	// watch is told about each read-only error. Nil watches nothing.
	watch readOnlyWatch
	// readOnlyTx is true in a transaction that the store opened READ ONLY. A
	// read-only error there can be a bug of the store, so the watch checks
	// the idle connections. In any other round trip, a read-only error shows
	// a session that refuses writes, so the watch resets the pool at once.
	readOnlyTx bool
}

// bound returns ctx bounded by the statement deadline. When ctx has an
// earlier deadline, that deadline and its cause apply.
func (rt roundTrip) bound(ctx context.Context) (context.Context, context.CancelFunc) {
	deadline := rt.deadline
	if deadline <= 0 {
		deadline = statementDeadline
	}

	return context.WithTimeoutCause(ctx, deadline, errStatementDeadline)
}

// report translates err of a round trip whose bounded context cause reads,
// and tells the watch about a read-only error.
func (rt roundTrip) report(cause func() error, err error) error {
	out := translateIn(cause, err)

	var unavailable *storeerr.Error
	if rt.watch == nil || !errors.As(out, &unavailable) || unavailable.Class() != storeerr.ReadOnly {
		return out
	}

	if rt.readOnlyTx {
		rt.watch.suspectReadOnly()
	} else {
		rt.watch.confirmReadOnly()
	}

	return out
}

// causeOf returns a function that reads the cause of ctx, so a wrapper can
// keep the cause of its bounded context without the context itself.
func causeOf(ctx context.Context) func() error {
	return func() error { return context.Cause(ctx) }
}

// translateIn is translate for an error of a round trip that ran under a
// bounded context. The function cause reads the cause of that context. The end of the statement deadline
// is a lost connection: pgx closed the connection at the deadline. A deadline
// of the caller stays the error of the caller.
func translateIn(cause func() error, err error) error {
	if err == nil || storeerr.Is(err) {
		return err
	}

	if errors.Is(err, context.DeadlineExceeded) && errors.Is(cause(), errStatementDeadline) {
		return storeerr.New(storeerr.ConnectionFailed, err)
	}

	return translate(err)
}

// readOnlyWatch is told about each error of the class ReadOnly (see
// poolGuard). Nil watches nothing.
type readOnlyWatch interface {
	// suspectReadOnly starts a check of the idle connections: the error came
	// from a transaction that the store opened READ ONLY.
	suspectReadOnly()
	// confirmReadOnly resets the pool under the limiter: the error came from
	// a session that refuses writes.
	confirmReadOnly()
}

// readOnlyAccess reports whether opts open a READ ONLY transaction, in the
// access mode or in a begin statement of its own. An unknown begin statement
// that names READ ONLY counts too, so the store never resets the pool for the
// error of its own read-only transaction.
func readOnlyAccess(opts pgx.TxOptions) bool {
	return opts.AccessMode == pgx.ReadOnly || strings.Contains(strings.ToUpper(opts.BeginQuery), "READ ONLY")
}

// guardedDB is the db.DBTX of the store pool. It bounds each statement by the
// statement deadline and translates its error, so no call site does.
type guardedDB struct {
	inner interface {
		Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
		Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
		QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	}
	rt roundTrip
}

func (g guardedDB) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	bounded, cancel := g.rt.bound(ctx)
	defer cancel()

	tag, err := g.inner.Exec(bounded, sql, args...)

	return tag, g.rt.report(causeOf(bounded), err)
}

func (g guardedDB) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	bounded, cancel := g.rt.bound(ctx)

	rows, err := g.inner.Query(bounded, sql, args...)
	if err != nil {
		cancel()

		return rows, g.rt.report(causeOf(bounded), err)
	}

	return guardedRows{Rows: rows, cause: causeOf(bounded), cancel: cancel, rt: g.rt}, nil
}

func (g guardedDB) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	bounded, cancel := g.rt.bound(ctx)

	return guardedRow{Row: g.inner.QueryRow(bounded, sql, args...), cause: causeOf(bounded), cancel: cancel, rt: g.rt}
}

// guardedRows keeps the statement deadline until the read of the rows ends,
// and translates the error that ends the read. The deadline ends at Close or
// at the last Next, not when Query returns.
type guardedRows struct {
	pgx.Rows

	cause  func() error
	cancel context.CancelFunc
	rt     roundTrip
}

func (r guardedRows) Next() bool {
	if r.Rows.Next() {
		return true
	}

	r.cancel()

	return false
}

func (r guardedRows) Close() {
	r.Rows.Close()
	r.cancel()
}

func (r guardedRows) Err() error { return r.rt.report(r.cause, r.Rows.Err()) }

// guardedRow keeps the statement deadline until Scan, and translates the
// error of a single-row read.
type guardedRow struct {
	pgx.Row

	cause  func() error
	cancel context.CancelFunc
	rt     roundTrip
}

func (r guardedRow) Scan(dest ...any) error {
	defer r.cancel()

	return r.rt.report(r.cause, r.Row.Scan(dest...))
}

// guardedPool starts the transactions of the store. The BEGIN has the
// statement deadline. Each transaction it starts bounds and translates its
// round trips, so a call site that uses the transaction does not.
type guardedPool struct {
	pool interface {
		Begin(ctx context.Context) (pgx.Tx, error)
		BeginTx(ctx context.Context, opts pgx.TxOptions) (pgx.Tx, error)
	}
	rt roundTrip
}

func (g guardedPool) Begin(ctx context.Context) (pgx.Tx, error) {
	bounded, cancel := g.rt.bound(ctx)
	defer cancel()

	tx, err := g.pool.Begin(bounded)
	if err != nil {
		return nil, g.rt.report(causeOf(bounded), err)
	}

	return guardedTx{Tx: tx, rt: g.rt}, nil
}

func (g guardedPool) BeginTx(ctx context.Context, opts pgx.TxOptions) (pgx.Tx, error) {
	bounded, cancel := g.rt.bound(ctx)
	defer cancel()

	tx, err := g.pool.BeginTx(bounded, opts)
	if err != nil {
		return nil, g.rt.report(causeOf(bounded), err)
	}

	rt := g.rt
	rt.readOnlyTx = readOnlyAccess(opts)

	return guardedTx{Tx: tx, rt: rt}, nil
}

// guardedTx bounds and translates each round trip of a transaction: the
// statements, the batches, the copies, the savepoints, the commit and the
// rollback. Conn returns the raw connection, which no wrapper can wrap; the
// test of the long paths finds each call of it.
type guardedTx struct {
	pgx.Tx

	rt roundTrip
}

func (t guardedTx) Begin(ctx context.Context) (pgx.Tx, error) {
	bounded, cancel := t.rt.bound(ctx)
	defer cancel()

	nested, err := t.Tx.Begin(bounded)
	if err != nil {
		return nil, t.rt.report(causeOf(bounded), err)
	}

	return guardedTx{Tx: nested, rt: t.rt}, nil
}

func (t guardedTx) Commit(ctx context.Context) error {
	bounded, cancel := t.rt.bound(ctx)
	defer cancel()

	return t.rt.report(causeOf(bounded), t.Tx.Commit(bounded))
}

func (t guardedTx) Rollback(ctx context.Context) error {
	bounded, cancel := t.rt.bound(ctx)
	defer cancel()

	return t.rt.report(causeOf(bounded), t.Tx.Rollback(bounded))
}

func (t guardedTx) CopyFrom(
	ctx context.Context, table pgx.Identifier, columns []string, src pgx.CopyFromSource,
) (int64, error) {
	bounded, cancel := t.rt.bound(ctx)
	defer cancel()

	n, err := t.Tx.CopyFrom(bounded, table, columns, src)

	return n, t.rt.report(causeOf(bounded), err)
}

func (t guardedTx) Prepare(ctx context.Context, name, sql string) (*pgconn.StatementDescription, error) {
	bounded, cancel := t.rt.bound(ctx)
	defer cancel()

	desc, err := t.Tx.Prepare(bounded, name, sql)

	return desc, t.rt.report(causeOf(bounded), err)
}

func (t guardedTx) SendBatch(ctx context.Context, b *pgx.Batch) pgx.BatchResults {
	bounded, cancel := t.rt.bound(ctx)

	return guardedBatch{BatchResults: t.Tx.SendBatch(bounded, b), cause: causeOf(bounded), cancel: cancel, rt: t.rt}
}

func (t guardedTx) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return t.inner().Exec(ctx, sql, args...)
}

func (t guardedTx) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return t.inner().Query(ctx, sql, args...)
}

func (t guardedTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return t.inner().QueryRow(ctx, sql, args...)
}

// inner returns the statement wrapper of the transaction.
func (t guardedTx) inner() guardedDB { return guardedDB{inner: t.Tx, rt: t.rt} }

// guardedBatch keeps the statement deadline until the batch closes, and
// translates the error of each result.
type guardedBatch struct {
	pgx.BatchResults

	cause  func() error
	cancel context.CancelFunc
	rt     roundTrip
}

func (b guardedBatch) Exec() (pgconn.CommandTag, error) {
	tag, err := b.BatchResults.Exec()

	return tag, b.rt.report(b.cause, err)
}

func (b guardedBatch) Query() (pgx.Rows, error) {
	rows, err := b.BatchResults.Query()
	if err != nil {
		return rows, b.rt.report(b.cause, err)
	}

	// The batch owns the deadline, so the rows end nothing at their close.
	return guardedRows{Rows: rows, cause: b.cause, cancel: func() {}, rt: b.rt}, nil
}

func (b guardedBatch) QueryRow() pgx.Row {
	return guardedRow{Row: b.BatchResults.QueryRow(), cause: b.cause, cancel: func() {}, rt: b.rt}
}

func (b guardedBatch) Close() error {
	defer b.cancel()

	return b.rt.report(b.cause, b.BatchResults.Close())
}

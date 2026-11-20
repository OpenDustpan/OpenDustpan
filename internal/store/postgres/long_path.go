package postgres

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The server timeouts of each connection of the pool. The pool sends them
// as start-up parameters, so RESET ALL goes back to them. A slow statement on
// a server that answers ends on the server first, with SQLSTATE 57014, and
// its locks go free; the connection stays in the pool. A backend whose client
// went away stops at the next check of the connection and frees its locks.
const (
	serverStatementTimeout = 25 * time.Second
	serverIdleInTxTimeout  = 60 * time.Second
	clientCheckInterval    = 5 * time.Second
)

// resetTimeout bounds the RESET ALL of a released long-path connection.
// AfterRelease gets no context, so the reset makes its own.
const resetTimeout = 2 * time.Second

// longBound is the server bounds of one kind of long path. The path sets
// both with SET LOCAL in its transaction, so they end with the transaction.
type longBound struct {
	// name names the kind of path in errors and logs.
	name string
	// statement is the statement_timeout of the transaction.
	statement time.Duration
	// idle is the idle_in_transaction_session_timeout of the transaction.
	idle time.Duration
}

// The bounds of the long paths. A backup streams rows to the object store
// between its statements, so its idle bound covers the upload of one part.
const (
	backupStatementTimeout = time.Hour
	backupIdleTimeout      = 15 * time.Minute
	schemaStatementTimeout = 10 * time.Minute
	schemaIdleTimeout      = time.Minute
)

// backupBound is the bound of a backup, a restore and the paths that copy
// rows of a backup.
func backupBound() longBound {
	return longBound{name: "backup", statement: backupStatementTimeout, idle: backupIdleTimeout}
}

// migrationBound is the bound of the migration transaction.
func migrationBound() longBound {
	return longBound{name: "migration", statement: migrationDeadline, idle: migrationDeadline}
}

// schemaBound is the bound of a CREATE SCHEMA or a DROP SCHEMA of a staging
// schema.
func schemaBound() longBound {
	return longBound{name: "schema change", statement: schemaStatementTimeout, idle: schemaIdleTimeout}
}

// serverTimeoutParams returns the start-up parameters of the server
// timeouts, in milliseconds.
func serverTimeoutParams() map[string]string {
	return map[string]string{
		"statement_timeout":                   strconv.FormatInt(serverStatementTimeout.Milliseconds(), 10),
		"idle_in_transaction_session_timeout": strconv.FormatInt(serverIdleInTxTimeout.Milliseconds(), 10),
		"client_connection_check_interval":    strconv.FormatInt(clientCheckInterval.Milliseconds(), 10),
	}
}

// longMarks holds the connections that a long path got from the pool. The
// AfterRelease function of the pool resets a marked connection before the
// next request gets it. An entry goes away at the release, at the destroy of
// the connection and before a hijack, so the set does not grow.
type longMarks struct {
	mu    sync.Mutex
	conns map[*pgx.Conn]struct{}
}

// newLongMarks returns an empty set.
func newLongMarks() *longMarks {
	return &longMarks{mu: sync.Mutex{}, conns: map[*pgx.Conn]struct{}{}}
}

// mark adds conn.
func (m *longMarks) mark(conn *pgx.Conn) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.conns[conn] = struct{}{}
}

// take removes conn and reports whether it was marked.
func (m *longMarks) take(conn *pgx.Conn) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	_, ok := m.conns[conn]
	delete(m.conns, conn)

	return ok
}

// size returns the number of marked connections.
func (m *longMarks) size() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	return len(m.conns)
}

// afterRelease is the AfterRelease function of the pool. It resets a marked
// connection with RESET ALL under resetTimeout, so no session value of a
// long path reaches the next request. On any error, or at the deadline, it
// returns false and the pool destroys the connection. A connection that
// stops answering then holds its slot for resetTimeout and the dial of one
// cancel request at most, not for the TCP timeout of the host. An unmarked
// connection goes back as it is: the API paths change no session value.
func (m *longMarks) afterRelease(conn *pgx.Conn) bool {
	if !m.take(conn) {
		return true
	}

	ctx, cancel := context.WithTimeout(context.Background(), resetTimeout)
	defer cancel()

	if _, err := conn.Exec(ctx, "RESET ALL"); err != nil {
		// pgx closes a broken connection in the background: it sends a cancel
		// request on a new connection, which the connect timeout bounds, and
		// then reads the old socket until the server closes it, for up to 15
		// seconds. A host that stopped answering never closes it, and the pool
		// holds the slot until that read ends. Closing the socket here ends
		// the read at once. The pool destroys the connection either way.
		if closeErr := conn.PgConn().Conn().Close(); closeErr != nil {
			return false
		}

		return false
	}

	return true
}

// beforeClose is the BeforeClose function of the pool. It drops the mark of
// a connection that the pool destroys.
func (m *longMarks) beforeClose(conn *pgx.Conn) {
	m.take(conn)
}

// longConn gets a connection for a long path from the pool and marks it, so
// the pool resets it at its release. A caller that hijacks the connection
// calls unmarkForHijack first.
func (s *Store) longConn(ctx context.Context) (*pgxpool.Conn, error) {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return nil, translate(fmt.Errorf("acquire a connection: %w", err))
	}

	s.marks.mark(conn.Conn())

	return conn, nil
}

// unmarkForHijack drops the mark of conn before a hijack takes it from the
// pool.
func (s *Store) unmarkForHijack(conn *pgxpool.Conn) {
	s.marks.take(conn.Conn())
}

// setLocalBounds sets both server bounds of bound with SET LOCAL in tx.
func setLocalBounds(ctx context.Context, tx pgx.Tx, bound longBound) error {
	const query = `SELECT set_config('statement_timeout', $1, true),` +
		` set_config('idle_in_transaction_session_timeout', $2, true)`

	statement := strconv.FormatInt(bound.statement.Milliseconds(), 10)
	idle := strconv.FormatInt(bound.idle.Milliseconds(), 10)

	if _, err := tx.Exec(ctx, query, statement, idle); err != nil {
		return fmt.Errorf("set the bounds of the %s: %w", bound.name, translate(err))
	}

	return nil
}

// inLongTx runs fn in one transaction with opts on a long-path connection,
// with the server bounds of bound. It commits on success and rolls back on
// failure. A single statement outside a transaction also runs through it, in
// a short transaction of its own.
func (s *Store) inLongTx(
	ctx context.Context, bound longBound, opts pgx.TxOptions, fn func(tx pgx.Tx) error,
) error {
	conn, err := s.longConn(ctx)
	if err != nil {
		return fmt.Errorf("%s connection: %w", bound.name, err)
	}

	defer conn.Release()

	tx, err := conn.BeginTx(ctx, opts)
	if err != nil {
		return fmt.Errorf("begin %s transaction: %w", bound.name, translate(err))
	}

	if err := setLocalBounds(ctx, tx, bound); err != nil {
		return errors.Join(err, rollback(ctx, tx, bound))
	}

	if err := fn(tx); err != nil {
		return errors.Join(err, rollback(ctx, tx, bound))
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit %s transaction: %w", bound.name, translate(err))
	}

	return nil
}

// rollback rolls tx back and names bound in its error.
func rollback(ctx context.Context, tx pgx.Tx, bound longBound) error {
	if err := tx.Rollback(ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
		return fmt.Errorf("roll back %s transaction: %w", bound.name, translate(err))
	}

	return nil
}

// defaultTx returns the options of a transaction with the defaults of the
// server.
func defaultTx() pgx.TxOptions {
	return pgx.TxOptions{IsoLevel: "", AccessMode: "", DeferrableMode: "", BeginQuery: "", CommitQuery: ""}
}

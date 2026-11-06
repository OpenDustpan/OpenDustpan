package postgres

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"opendustpan.org/opendustpan/pkg/namespace"
)

// resetInterval is the shortest time between two resets of the pool. The
// read-only check of a write error and the check of the readiness probe share
// it, so a burst of errors closes the pool once.
const resetInterval = 5 * time.Second

// writableCheckTimeout bounds the check of each connection that a read-only
// error starts.
const writableCheckTimeout = 2 * time.Second

// ResetReasonReadOnly is the reason of a reset of the pool after the pool
// held a connection to a server that refuses writes.
const ResetReasonReadOnly = "read-only"

// poolGuard resets the pool when it holds a connection to a server that
// refuses writes. A write that fails with SQLSTATE 25006 starts a check of the
// idle connections. The pool resets only when a connection fails the check,
// so a 25006 from a transaction that the store opened READ ONLY, which is a
// bug and not a read-only server, does not reset the pool.
type poolGuard struct {
	pool   *pgxpool.Pool
	logger *slog.Logger
	now    func() time.Time

	mu        sync.Mutex
	lastReset time.Time
	checking  bool
	closed    bool
	wg        sync.WaitGroup

	resets atomic.Uint64
}

// newPoolGuard returns the guard of pool.
func newPoolGuard(pool *pgxpool.Pool, logger *slog.Logger) *poolGuard {
	return &poolGuard{
		pool:      pool,
		logger:    logger,
		now:       time.Now,
		mu:        sync.Mutex{},
		lastReset: time.Time{},
		checking:  false,
		closed:    false,
		wg:        sync.WaitGroup{},
		resets:    atomic.Uint64{},
	}
}

// suspectReadOnly starts one check of the idle connections in the background.
// It does nothing while a check runs, after a reset in the last
// resetInterval, or after close.
func (g *poolGuard) suspectReadOnly() {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.closed || g.checking || g.recentReset() {
		return
	}

	g.checking = true

	g.wg.Go(func() {
		defer func() {
			g.mu.Lock()
			g.checking = false
			g.mu.Unlock()
		}()

		ctx, cancel := context.WithTimeout(context.Background(), writableCheckTimeout)
		defer cancel()

		if g.idleReadOnly(ctx) {
			g.reset(ctx)
		}
	})
}

// confirmReadOnly resets the pool under the limiter. A read-only error of a
// round trip outside a READ ONLY transaction of the store shows a session that
// refuses writes. The connection of that round trip is still in use then, so a
// check of the idle connections can miss it; the reset needs no check. The
// pool closes that connection when its request gives it back.
func (g *poolGuard) confirmReadOnly() {
	g.reset(context.Background())
}

// recentReset reports whether the last reset is less than resetInterval ago.
// The caller holds mu.
func (g *poolGuard) recentReset() bool {
	return !g.lastReset.IsZero() && g.now().Sub(g.lastReset) < resetInterval
}

// idleReadOnly checks each idle connection and reports whether one of them
// reaches a server that refuses writes. A connection whose check fails for
// another reason is not counted: the pool replaces a broken connection by
// itself.
func (g *poolGuard) idleReadOnly(ctx context.Context) bool {
	found := false

	for _, conn := range g.pool.AcquireAllIdle(ctx) {
		results, err := conn.Conn().PgConn().Exec(ctx, writableQuery).ReadAll()
		if err == nil && errors.Is(writableAnswer(results), errNotWritable) {
			found = true
		}

		conn.Release()
	}

	return found
}

// reset resets the pool unless it reset in the last resetInterval. It logs
// each reset with its reason and counts it.
func (g *poolGuard) reset(ctx context.Context) {
	g.mu.Lock()

	if g.closed || g.recentReset() {
		g.mu.Unlock()

		return
	}

	g.lastReset = g.now()
	g.mu.Unlock()

	g.pool.Reset()
	g.resets.Add(1)
	g.logger.WarnContext(ctx, "database connection pool reset: a connection reached a server that refuses writes",
		slog.String("reason", ResetReasonReadOnly))
}

// close stops new checks and waits for a running check.
func (g *poolGuard) close() {
	g.mu.Lock()
	g.closed = true
	g.mu.Unlock()

	g.wg.Wait()
}

// checkWritable checks one connection of the pool. It returns false when the
// server refuses writes, and then resets the pool under the same limiter as a
// read-only error. It returns an error that wraps namespace.ErrUnavailable,
// with no driver text, when the check cannot run. It fails closed: an answer
// of an unexpected shape counts as a server that refuses writes.
func (g *poolGuard) checkWritable(ctx context.Context) (bool, error) {
	conn, err := g.pool.Acquire(ctx)
	if errors.Is(err, errNotWritable) {
		// After a reset, each new connection to a server that refuses writes
		// fails the read-write check of the pool, so the answer is the same.
		return false, nil
	}

	if err != nil {
		// A deadline or a cancel of the caller stays the error of the caller.
		return false, fmt.Errorf("check that the database accepts writes: %w", translate(err))
	}

	results, err := conn.Conn().PgConn().Exec(ctx, writableQuery).ReadAll()
	conn.Release()

	if err != nil {
		return false, fmt.Errorf("check that the database accepts writes: %w", namespace.ErrUnavailable)
	}

	writable := writableAnswer(results) == nil
	if !writable {
		g.reset(ctx)
	}

	return writable, nil
}

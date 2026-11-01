package postgres

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/internal/storeerr"
)

// testURLEnv names the database URL of the PostgreSQL tests.
const testURLEnv = "DUSTPAN_TEST_POSTGRES_URL"

// shortDeadline is the statement deadline of the deadline tests.
const shortDeadline = 300 * time.Millisecond

// deadlineBound is the time within which a call behind a stalled connection
// must end: the deadline and a margin for a slow test host.
const deadlineBound = 5 * time.Second

// testURL returns the database URL of the PostgreSQL tests, or skips.
func testURL(t *testing.T) string {
	t.Helper()

	base := os.Getenv(testURLEnv)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL suite", testURLEnv)
	}

	return base
}

// holdProxy is a TCP proxy to PostgreSQL that can stop forwarding bytes in
// both directions while it keeps every connection open, like a network path
// to a host that stopped answering.
type holdProxy struct {
	listener net.Listener
	target   string

	mu      sync.Mutex
	held    bool
	release chan struct{}
	closed  bool
	conns   []net.Conn
	wg      sync.WaitGroup
}

// startHoldProxy starts a proxy to the host of base and returns it with the
// URL that reaches the database through it.
func startHoldProxy(t *testing.T, base string) (*holdProxy, string) {
	t.Helper()

	u, err := url.Parse(base)
	if err != nil {
		t.Fatalf("parse the database URL: %v", err)
	}

	var lc net.ListenConfig

	listener, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	p := &holdProxy{
		listener: listener, target: u.Host, mu: sync.Mutex{}, held: false,
		release: make(chan struct{}), closed: false, conns: nil, wg: sync.WaitGroup{},
	}
	p.wg.Go(p.accept)
	t.Cleanup(p.close)

	u.Host = listener.Addr().String()

	return p, u.String()
}

// accept forwards each accepted connection until the listener closes.
func (p *holdProxy) accept() {
	for {
		client, err := p.listener.Accept()
		if err != nil {
			return
		}

		var dialer net.Dialer

		server, err := dialer.Dial("tcp", p.target)
		if err != nil {
			closeQuietly(client)

			continue
		}

		p.mu.Lock()
		p.conns = append(p.conns, client, server)
		p.mu.Unlock()

		p.wg.Go(func() { p.pipe(server, client) })
		p.wg.Go(func() { p.pipe(client, server) })
	}
}

// pipe copies from src to dst, and waits before each write while the proxy
// holds the bytes.
func (p *holdProxy) pipe(dst, src net.Conn) {
	buf := make([]byte, 32*1024)

	for {
		n, err := src.Read(buf)
		if n > 0 {
			p.mu.Lock()
			held, release := p.held, p.release
			p.mu.Unlock()

			if held {
				<-release
			}

			if _, writeErr := dst.Write(buf[:n]); writeErr != nil {
				return
			}
		}

		if err != nil {
			return
		}
	}
}

// hold stops forwarding bytes. No connection closes.
func (p *holdProxy) hold() {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.held = true
}

// close stops the proxy and closes every connection.
func (p *holdProxy) close() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()

		return
	}

	p.closed = true
	close(p.release)

	closeQuietly(p.listener)

	for _, conn := range p.conns {
		closeQuietly(conn)
	}
	p.mu.Unlock()

	p.wg.Wait()
}

// connectCheck says whether a test pool keeps the read-write check of each
// new connection.
type connectCheck int

// The connect checks of a test pool.
const (
	// withCheck keeps the check of the store.
	withCheck connectCheck = iota
	// withoutCheck drops it, so the pool can hold read-only sessions, as a
	// pool does whose sessions turned read-only after they opened.
	withoutCheck
)

// testPool opens a pool at dbURL with the pool configuration of the store
// and the given runtime parameters, and closes it at the end of the test.
func testPool(t *testing.T, dbURL string, params map[string]string, check connectCheck) *pgxpool.Pool {
	t.Helper()

	cfg, _, err := poolConfig(dbURL)
	if err != nil {
		t.Fatalf("poolConfig: %v", err)
	}

	maps.Copy(cfg.ConnConfig.RuntimeParams, params)

	if check == withoutCheck {
		cfg.ConnConfig.ValidateConnect = nil
	}

	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatalf("open the pool: %v", err)
	}

	t.Cleanup(pool.Close)

	return pool
}

// deadlineCase is one round trip that a stalled connection must end within
// the statement deadline. prepare runs before the proxy holds the bytes, and
// call runs after.
type deadlineCase struct {
	name    string
	prepare func(ctx context.Context, t *testing.T, g guardedPool) pgx.Tx
	call    func(ctx context.Context, g guardedPool, tx pgx.Tx) error
}

// beginTx starts a transaction for the cases that need one.
func beginTx(ctx context.Context, t *testing.T, g guardedPool) pgx.Tx {
	t.Helper()

	tx, err := g.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	t.Cleanup(func() { rollbackQuietly(context.WithoutCancel(ctx), tx) })

	return tx
}

// noTx prepares nothing.
func noTx(context.Context, *testing.T, guardedPool) pgx.Tx { return nil }

// deadlineCases returns one case for each method of the guarded wrappers.
func deadlineCases() []deadlineCase {
	return []deadlineCase{
		{"begin", noTx, func(ctx context.Context, g guardedPool, _ pgx.Tx) error {
			tx, err := g.Begin(ctx)
			if err == nil {
				rollbackQuietly(ctx, tx)
			}

			return err
		}},
		{"exec", beginTx, func(ctx context.Context, _ guardedPool, tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, "SELECT 1"); err != nil {
				return fmt.Errorf("exec: %w", err)
			}

			return nil
		}},
		{"query and its rows", beginTx, func(ctx context.Context, _ guardedPool, tx pgx.Tx) error {
			rows, err := tx.Query(ctx, "SELECT 1")
			if err != nil {
				return fmt.Errorf("query: %w", err)
			}

			for rows.Next() {
				continue
			}

			rows.Close()

			return rows.Err()
		}},
		{"query row", beginTx, func(ctx context.Context, _ guardedPool, tx pgx.Tx) error {
			var n int

			return tx.QueryRow(ctx, "SELECT 1").Scan(&n)
		}},
		{"send batch", beginTx, func(ctx context.Context, _ guardedPool, tx pgx.Tx) error {
			var batch pgx.Batch

			batch.Queue("SELECT 1")

			results := tx.SendBatch(ctx, &batch)
			_, err := results.Exec()

			return errors.Join(err, results.Close())
		}},
		{"copy from", beginTx, func(ctx context.Context, _ guardedPool, tx pgx.Tx) error {
			rows := pgx.CopyFromRows([][]any{{1}})
			if _, err := tx.CopyFrom(ctx, pgx.Identifier{"pg_temp", "w9_copy"}, []string{"n"}, rows); err != nil {
				return fmt.Errorf("copy from: %w", err)
			}

			return nil
		}},
		{"savepoint", beginTx, func(ctx context.Context, _ guardedPool, tx pgx.Tx) error {
			if _, err := tx.Begin(ctx); err != nil {
				return fmt.Errorf("savepoint: %w", err)
			}

			return nil
		}},
		{"commit", beginTx, func(ctx context.Context, _ guardedPool, tx pgx.Tx) error {
			return tx.Commit(ctx)
		}},
		{"rollback", beginTx, func(ctx context.Context, _ guardedPool, tx pgx.Tx) error {
			return tx.Rollback(ctx)
		}},
	}
}

// TestStatementDeadlineClosesStalledConnection proves that each method of
// the guarded wrappers ends within the statement deadline when the connection
// stops answering and does not close, and that the error is a lost
// connection: it wraps namespace.ErrUnavailable and namespace.ErrUnreachable.
// The caller gives no deadline, so only the statement deadline can end it.
func TestStatementDeadlineClosesStalledConnection(t *testing.T) {
	t.Parallel()

	base := testURL(t)

	for _, tc := range deadlineCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			proxy, proxied := startHoldProxy(t, base)
			pool := testPool(t, proxied, nil, withCheck)
			g := guardedPool{pool: pool, rt: roundTrip{deadline: shortDeadline, watch: nil}}
			ctx := context.WithoutCancel(t.Context())

			tx := tc.prepare(ctx, t, g)
			if tx != nil {
				if _, err := tx.Exec(ctx, "CREATE TEMP TABLE w9_copy (n int)"); err != nil {
					t.Fatalf("make the temp table: %v", err)
				}
			}

			proxy.hold()

			start := time.Now()
			err := tc.call(ctx, g, tx)

			if elapsed := time.Since(start); elapsed > deadlineBound {
				t.Errorf("the call took %s, want at most %s", elapsed, deadlineBound)
			}

			if !errors.Is(err, namespace.ErrUnavailable) || !errors.Is(err, namespace.ErrUnreachable) {
				t.Fatalf("err = %v, want ErrUnavailable and ErrUnreachable", err)
			}
		})
	}
}

// TestStatementDeadlineKeepsCallerDeadline proves that a deadline of the
// caller stays the error of the caller: it is not a lost connection, so the
// API does not answer 503 for a request that ran out of its own time.
func TestStatementDeadlineKeepsCallerDeadline(t *testing.T) {
	t.Parallel()

	pool := testPool(t, testURL(t), nil, withCheck)
	g := guardedDB{inner: pool, rt: roundTrip{deadline: time.Minute, watch: nil}}

	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()

	_, err := g.Exec(ctx, "SELECT pg_sleep(5)")
	if err == nil {
		t.Fatal("pg_sleep(5) under a deadline of 200 ms answered no error")
	}

	if storeerr.Is(err) {
		t.Fatalf("err = %v, want the deadline of the caller, not a store error", err)
	}
}

// TestStatementDeadlineAllowsLongRowRead proves that the deadline of a query
// does not end when Query returns: rows read after Query returns still work.
func TestStatementDeadlineAllowsLongRowRead(t *testing.T) {
	t.Parallel()

	pool := testPool(t, testURL(t), nil, withCheck)
	g := guardedDB{inner: pool, rt: roundTrip{deadline: time.Minute, watch: nil}}

	rows, err := g.Query(t.Context(), "SELECT n FROM generate_series(1, 1000) AS n")
	if err != nil {
		t.Fatalf("query: %v", err)
	}

	count := 0
	for rows.Next() {
		count++
	}

	rows.Close()

	if rows.Err() != nil || count != 1000 {
		t.Fatalf("read %d rows, err %v, want 1000 rows and no error", count, rows.Err())
	}
}

// failWrite runs one write in a transaction with opts, and fails the test
// unless the write fails with a read-only error.
func failWrite(t *testing.T, g guardedPool, opts pgx.TxOptions) {
	t.Helper()

	err := pgx.BeginTxFunc(t.Context(), g, opts, func(tx pgx.Tx) error {
		if _, execErr := tx.Exec(t.Context(), "CREATE TEMP TABLE w9_ro (n int)"); execErr != nil {
			return fmt.Errorf("write: %w", execErr)
		}

		return nil
	})
	if !errors.Is(err, namespace.ErrUnreachable) {
		t.Fatalf("write = %v, want a read-only error that wraps ErrUnreachable", err)
	}
}

// defaultAccess starts a transaction with no access mode, so the default of
// the session applies. An explicit READ WRITE would override the read-only
// default of the session.
const defaultAccess pgx.TxAccessMode = ""

// txOptions returns the options of a transaction in access mode.
func txOptions(mode pgx.TxAccessMode) pgx.TxOptions {
	return pgx.TxOptions{IsoLevel: "", AccessMode: mode, DeferrableMode: "", BeginQuery: "", CommitQuery: ""}
}

// readOnlyParams makes each session of a pool read-only, as on a server with
// default_transaction_read_only = on.
func readOnlyParams() map[string]string {
	return map[string]string{"default_transaction_read_only": "on"}
}

// warmPool opens n connections of pool and gives them back, so they are idle.
func warmPool(t *testing.T, pool *pgxpool.Pool, n int) {
	t.Helper()

	conns := make([]*pgxpool.Conn, 0, n)

	for range n {
		conn, err := pool.Acquire(t.Context())
		if err != nil {
			t.Fatalf("acquire: %v", err)
		}

		conns = append(conns, conn)
	}

	for _, conn := range conns {
		conn.Release()
	}
}

// TestReadOnlyErrorResetsPool proves that a write that fails with SQLSTATE
// 25006 resets the pool when an idle connection reaches a server that
// refuses writes, that a burst of such errors resets it once, and that a
// 25006 from a transaction that the store opened READ ONLY does not reset a
// pool of writable connections.
func TestReadOnlyErrorResetsPool(t *testing.T) {
	t.Parallel()

	base := testURL(t)
	logger := slog.New(slog.DiscardHandler)

	tests := []struct {
		name      string
		params    map[string]string
		txOptions pgx.TxOptions
		errors    int
		want      uint64
	}{
		{"read-only server", readOnlyParams(), txOptions(defaultAccess), 1, 1},
		{"burst of errors", readOnlyParams(), txOptions(defaultAccess), 20, 1},
		{"read only transaction of the store", nil, txOptions(pgx.ReadOnly), 3, 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			pool := testPool(t, base, tc.params, withoutCheck)
			warmPool(t, pool, 3)

			guard := newPoolGuard(pool, logger)
			g := guardedPool{pool: pool, rt: roundTrip{deadline: time.Minute, watch: guard}}

			for range tc.errors {
				failWrite(t, g, tc.txOptions)
			}

			// Wait for the running check. A close would stop the reset.
			guard.wg.Wait()
			guard.close()

			if got := guard.resets.Load(); got != tc.want {
				t.Fatalf("resets = %d, want %d", got, tc.want)
			}
		})
	}
}

// TestCheckWritable proves the write check of the readiness probe: true on a
// writable server, false on a server whose sessions are read-only, a reset of
// the pool for the read-only server, and one reset only within the limiter.
func TestCheckWritable(t *testing.T) {
	t.Parallel()

	base := testURL(t)
	logger := slog.New(slog.DiscardHandler)

	tests := []struct {
		name       string
		params     map[string]string
		want       bool
		wantResets uint64
	}{
		{"writable", nil, true, 0},
		{"read-only sessions", readOnlyParams(), false, 1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			guard := newPoolGuard(testPool(t, base, tc.params, withoutCheck), logger)
			defer guard.close()

			for range 3 {
				ok, err := guard.checkWritable(t.Context())
				if err != nil || ok != tc.want {
					t.Fatalf("checkWritable = %t, %v, want %t and no error", ok, err, tc.want)
				}
			}

			if got := guard.resets.Load(); got != tc.wantResets {
				t.Fatalf("resets = %d, want %d", got, tc.wantResets)
			}
		})
	}
}

// TestCheckWritableFailsWhenTheServerIsGone proves that a check that cannot
// run gives an error that wraps namespace.ErrUnavailable and holds no driver
// text, and does not answer false: the probe then names the store, not a
// read-only server.
func TestCheckWritableFailsWhenTheServerIsGone(t *testing.T) {
	t.Parallel()

	proxy, proxied := startHoldProxy(t, testURL(t))
	pool := testPool(t, proxied, nil, withCheck)
	guard := newPoolGuard(pool, slog.New(slog.DiscardHandler))

	defer guard.close()

	proxy.close()

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()

	ok, err := guard.checkWritable(ctx)
	if !errors.Is(err, namespace.ErrUnavailable) || ok {
		t.Fatalf("checkWritable = %t, %v, want false and ErrUnavailable", ok, err)
	}

	if err.Error() != "check that the database accepts writes: database unavailable: connection failed" {
		t.Errorf("error text %q, want no driver text", err)
	}
}

// TestWritableAnswerFailsClosed proves that an answer of an unexpected shape
// or value counts as a server that refuses writes.
func TestWritableAnswerFailsClosed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		rows [][][]byte
		want error
	}{
		{"writable", [][][]byte{{[]byte("f"), []byte("off")}}, nil},
		{"in recovery", [][][]byte{{[]byte("t"), []byte("on")}}, errNotWritable},
		{"read-only default", [][][]byte{{[]byte("f"), []byte("on")}}, errNotWritable},
		{"no row", nil, errNotWritable},
		{"two rows", [][][]byte{{[]byte("f"), []byte("off")}, {[]byte("f"), []byte("off")}}, errNotWritable},
		{"one column", [][][]byte{{[]byte("f")}}, errNotWritable},
		{"unknown value", [][][]byte{{[]byte("maybe"), []byte("off")}}, errNotWritable},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			result := new(pgconn.Result)
			result.Rows = tc.rows

			err := writableAnswer([]*pgconn.Result{result})
			if !errors.Is(err, tc.want) || (tc.want == nil && err != nil) {
				t.Fatalf("writableAnswer = %v, want %v", err, tc.want)
			}
		})
	}
}

// TestWritableAnswerRefusesNoResult proves that an answer with no result
// counts as a server that refuses writes.
func TestWritableAnswerRefusesNoResult(t *testing.T) {
	t.Parallel()

	if err := writableAnswer(nil); !errors.Is(err, errNotWritable) {
		t.Fatalf("writableAnswer(nil) = %v, want errNotWritable", err)
	}
}

// closeQuietly closes c in a cleanup path, where a close error changes
// nothing.
func closeQuietly(c io.Closer) {
	if err := c.Close(); err != nil {
		return
	}
}

// rollbackQuietly rolls tx back in a cleanup path. The case already rolled
// back or lost the connection, so the error changes nothing.
func rollbackQuietly(ctx context.Context, tx pgx.Tx) {
	if err := tx.Rollback(ctx); err != nil {
		return
	}
}

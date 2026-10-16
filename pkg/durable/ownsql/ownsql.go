// Package ownsql is our own minimal adapter of the durable port: two tables,
// a claim per operation with a token and an expiry from the database clock,
// and schedules claimed with one conditional UPDATE. It runs on PostgreSQL
// (pgx stdlib) and SQLite (modernc) through database/sql.
package ownsql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/durable"
)

// Dialect selects the SQL of the adapter.
type Dialect int

// Dialects.
const (
	Postgres Dialect = iota
	SQLite
)

// Options tunes an instance. Zero values take the defaults.
type Options struct {
	ClaimTTL    time.Duration // default 2 s
	RenewEvery  time.Duration // default TTL/3
	Poll        time.Duration // default 50 ms
	MaxAttempts int           // default 3
	Logger      *slog.Logger
}

// Engine is one instance of the adapter.
type Engine struct {
	db       *sql.DB
	dialect  Dialect
	holder   string
	kinds    map[string]durable.Kind
	sched    []durable.Schedule
	opt      Options
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	closeErr error
}

var _ durable.Engine = (*Engine)(nil)

// Migrate creates the tables. Idempotent.
func Migrate(ctx context.Context, db *sql.DB, d Dialect) error {
	blob := "BYTEA"
	if d == SQLite {
		blob = "BLOB"
	}

	stmts := []string{
		`CREATE TABLE IF NOT EXISTS durable_operations (
			id TEXT PRIMARY KEY,
			start_key TEXT NOT NULL UNIQUE,
			kind TEXT NOT NULL,
			input ` + blob + `,
			state TEXT NOT NULL,
			steps_done INTEGER NOT NULL DEFAULT 0,
			holder TEXT NOT NULL DEFAULT '',
			token BIGINT NOT NULL DEFAULT 0,
			claim_until_ms BIGINT NOT NULL DEFAULT 0,
			attempts INTEGER NOT NULL DEFAULT 0,
			error TEXT NOT NULL DEFAULT '',
			created_ms BIGINT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS durable_schedules (
			name TEXT PRIMARY KEY,
			next_run_ms BIGINT NOT NULL
		)`,
	}

	for _, s := range stmts {
		if _, err := db.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
	}

	return nil
}

// New starts an instance named holder with its workers.
func New(db *sql.DB, d Dialect, holder string, kinds []durable.Kind, sched []durable.Schedule, opt Options) *Engine {
	if opt.ClaimTTL == 0 {
		opt.ClaimTTL = 2 * time.Second
	}

	if opt.RenewEvery == 0 {
		opt.RenewEvery = opt.ClaimTTL / 3
	}

	if opt.Poll == 0 {
		opt.Poll = 50 * time.Millisecond
	}

	if opt.MaxAttempts == 0 {
		opt.MaxAttempts = 3
	}

	if opt.Logger == nil {
		opt.Logger = slog.New(slog.DiscardHandler)
	}

	e := &Engine{db: db, dialect: d, holder: holder, kinds: map[string]durable.Kind{}, sched: sched, opt: opt}
	for _, k := range kinds {
		e.kinds[k.Name] = k
	}

	ctx, cancel := context.WithCancel(context.Background())
	e.cancel = cancel

	e.wg.Go(func() { e.workLoop(ctx) })

	if len(sched) > 0 {
		e.wg.Go(func() { e.scheduleLoop(ctx) })
	}

	return e
}

// now is the database clock in Unix milliseconds. PostgreSQL uses
// clock_timestamp(), not now(), so a long transaction sees the current time.
func (e *Engine) now() string {
	if e.dialect == SQLite {
		return `CAST((julianday('now') - 2440587.5) * 86400000 AS INTEGER)`
	}

	return `(extract(epoch from clock_timestamp()) * 1000)::bigint`
}

// q rebinds ? placeholders for PostgreSQL.
func (e *Engine) q(s string) string {
	return rebind(e.dialect, s)
}

func rebind(d Dialect, s string) string {
	if d != Postgres {
		return s
	}

	var b strings.Builder

	n := 0

	for _, r := range s {
		if r == '?' {
			n++
			b.WriteString("$" + strconv.Itoa(n))

			continue
		}

		b.WriteRune(r)
	}

	return b.String()
}

// Start implements durable.Engine.
func (e *Engine) Start(ctx context.Context, key, kind string, input []byte) (string, error) {
	return e.startIn(ctx, e.db, key, kind, input)
}

type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func (e *Engine) startIn(ctx context.Context, x execer, key, kind string, input []byte) (string, error) {
	id := ident.NewID("op-")

	_, err := x.ExecContext(ctx, e.q(`INSERT INTO durable_operations (id, start_key, kind, input, state, created_ms)
		VALUES (?, ?, ?, ?, 'pending', `+e.now()+`) ON CONFLICT (start_key) DO NOTHING`), id, key, kind, input)
	if err != nil {
		return "", fmt.Errorf("start: %w", err)
	}

	var got string
	if err := x.QueryRowContext(ctx, e.q(`SELECT id FROM durable_operations WHERE start_key = ?`), key).Scan(&got); err != nil {
		return "", fmt.Errorf("start: read id: %w", err)
	}

	return got, nil
}

// Get implements durable.Engine.
func (e *Engine) Get(ctx context.Context, id string) (durable.Status, error) {
	var s durable.Status

	err := e.db.QueryRowContext(ctx, e.q(`SELECT id, kind, state, steps_done, holder, error FROM durable_operations WHERE id = ?`), id).
		Scan(&s.ID, &s.Kind, &s.State, &s.StepsDone, &s.Holder, &s.Error)
	if errors.Is(err, sql.ErrNoRows) {
		return s, durable.ErrNotFound
	}

	if err != nil {
		return s, fmt.Errorf("get: %w", err)
	}

	return s, nil
}

// Close implements durable.Engine.
func (e *Engine) Close(_ context.Context) error {
	e.cancel()
	e.wg.Wait()

	return e.closeErr
}

type claim struct {
	id    string
	kind  string
	input []byte
	done  int
	token int64
}

// claimOne takes one claimable operation in a short transaction and commits.
func (e *Engine) claimOne(ctx context.Context) (claim, bool, error) {
	lock := ` FOR UPDATE SKIP LOCKED`
	if e.dialect == SQLite {
		lock = ``
	}

	query := e.q(`UPDATE durable_operations
		SET holder = ?, token = token + 1, claim_until_ms = ` + e.now() + ` + ?, state = 'running', attempts = attempts + 1
		WHERE id = (SELECT id FROM durable_operations
			WHERE state IN ('pending', 'running') AND claim_until_ms < ` + e.now() + `
			ORDER BY created_ms LIMIT 1` + lock + `)
		RETURNING id, kind, input, steps_done, token`)

	var c claim

	err := e.db.QueryRowContext(ctx, query, e.holder, e.opt.ClaimTTL.Milliseconds()).
		Scan(&c.id, &c.kind, &c.input, &c.done, &c.token)
	if errors.Is(err, sql.ErrNoRows) {
		return c, false, nil
	}

	if err != nil {
		return c, false, fmt.Errorf("claim: %w", err)
	}

	return c, true, nil
}

func (e *Engine) workLoop(ctx context.Context) {
	t := time.NewTicker(e.opt.Poll)
	defer t.Stop()

	for {
		c, ok, err := e.claimOne(ctx)
		if err != nil && ctx.Err() == nil {
			e.opt.Logger.Warn("claim failed", slog.String("error", err.Error()))
		}

		if ok {
			e.run(ctx, c)

			continue
		}

		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// run runs the remaining steps of c while a renewer extends the claim.
func (e *Engine) run(ctx context.Context, c claim) {
	k, ok := e.kinds[c.kind]
	if !ok {
		return
	}

	rctx, stop := context.WithCancel(ctx)
	defer stop()

	var rw sync.WaitGroup

	rw.Go(func() { e.renew(rctx, c) })
	defer rw.Wait()
	defer stop()

	for i := c.done; i < len(k.Steps); i++ {
		if err := e.runStep(ctx, c, i, k.Steps[i]); err != nil {
			if errors.Is(err, durable.ErrClaimLost) || ctx.Err() != nil {
				return
			}

			e.failAttempt(ctx, c, err)

			return
		}
	}

	_, _ = e.db.ExecContext(ctx, e.q(`UPDATE durable_operations SET state = 'succeeded', holder = '', claim_until_ms = 0
		WHERE id = ? AND token = ?`), c.id, c.token)
}

func (e *Engine) renew(ctx context.Context, c claim) {
	t := time.NewTicker(e.opt.RenewEvery)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			_, _ = e.db.ExecContext(ctx, e.q(`UPDATE durable_operations SET claim_until_ms = `+e.now()+` + ?
				WHERE id = ? AND token = ?`), e.opt.ClaimTTL.Milliseconds(), c.id, c.token)
		}
	}
}

// runStep runs step i in one transaction with the step record: the record
// update checks the claim token and the step index, so a holder that lost
// its claim rolls back its domain write too.
func (e *Engine) runStep(ctx context.Context, c claim, i int, s durable.Step) error {
	tx, err := e.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("step %s: begin: %w", s.Name, err)
	}

	defer func() { _ = tx.Rollback() }()

	if err := s.Run(ctx, txAdapter{tx: tx, d: e.dialect}, c.input); err != nil {
		return fmt.Errorf("step %s: %w", s.Name, err)
	}

	res, err := tx.ExecContext(ctx, e.q(`UPDATE durable_operations SET steps_done = steps_done + 1, claim_until_ms = `+e.now()+` + ?
		WHERE id = ? AND token = ? AND steps_done = ?`), e.opt.ClaimTTL.Milliseconds(), c.id, c.token, i)
	if err != nil {
		return fmt.Errorf("step %s: record: %w", s.Name, err)
	}

	if n, _ := res.RowsAffected(); n == 0 {
		return durable.ErrClaimLost
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("step %s: commit: %w", s.Name, err)
	}

	return nil
}

func (e *Engine) failAttempt(ctx context.Context, c claim, stepErr error) {
	backoff := 100 * time.Millisecond

	_, _ = e.db.ExecContext(ctx, e.q(`UPDATE durable_operations
		SET error = ?, holder = '',
			state = CASE WHEN attempts >= ? THEN 'failed' ELSE 'running' END,
			claim_until_ms = `+e.now()+` + ?
		WHERE id = ? AND token = ?`), stepErr.Error(), e.opt.MaxAttempts, backoff.Milliseconds(), c.id, c.token)
}

// scheduleLoop fires each schedule once per period across all instances:
// one conditional UPDATE moves next_run_ms forward, and the operation start
// commits in the same transaction with a key per tick.
func (e *Engine) scheduleLoop(ctx context.Context) {
	for _, s := range e.sched {
		_, _ = e.db.ExecContext(ctx, e.q(`INSERT INTO durable_schedules (name, next_run_ms) VALUES (?, `+e.now()+`)
			ON CONFLICT (name) DO NOTHING`), s.Name)
	}

	t := time.NewTicker(e.opt.Poll)
	defer t.Stop()

	for {
		for _, s := range e.sched {
			e.fire(ctx, s)
		}

		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (e *Engine) fire(ctx context.Context, s durable.Schedule) {
	tx, err := e.db.BeginTx(ctx, nil)
	if err != nil {
		return
	}

	defer func() { _ = tx.Rollback() }()

	var tick int64

	err = tx.QueryRowContext(ctx, e.q(`UPDATE durable_schedules SET next_run_ms = next_run_ms + ?
		WHERE name = ? AND next_run_ms <= `+e.now()+` RETURNING next_run_ms - ?`),
		s.Every.Milliseconds(), s.Name, s.Every.Milliseconds()).Scan(&tick)
	if err != nil {
		return
	}

	if _, err := e.startIn(ctx, tx, s.Name+"@"+strconv.FormatInt(tick, 10), s.Kind, nil); err != nil {
		return
	}

	_ = tx.Commit()
}

type txAdapter struct {
	tx *sql.Tx
	d  Dialect
}

func (t txAdapter) Exec(ctx context.Context, query string, args ...any) (int64, error) {
	res, err := t.tx.ExecContext(ctx, rebind(t.d, query), args...)
	if err != nil {
		return 0, err //nolint:wrapcheck // Spike.
	}

	return res.RowsAffected() //nolint:wrapcheck // Spike.
}

func (t txAdapter) QueryRow(ctx context.Context, query string, args ...any) durable.Row {
	return t.tx.QueryRowContext(ctx, rebind(t.d, query), args...)
}

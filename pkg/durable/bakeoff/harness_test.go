package bakeoff_test

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib" // pgx database/sql driver.
	_ "modernc.org/sqlite"             // SQLite driver.

	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/durable/dbosadapter"
	"opendustpan.org/opendustpan/pkg/durable/ownsql"
)

// backing is one database for one test: the domain tables plus whatever the
// adapter creates. It can open instances in this process, and describes
// itself in environment variables so a helper process opens the same one.
type backing struct {
	adapter string // "own" or "dbos"
	store   string // "postgres" or "sqlite"
	url     string // PostgreSQL schema URL
	schema  string // PostgreSQL test schema
	path    string // SQLite file
	db      *sql.DB
}

func (b *backing) dialect() ownsql.Dialect {
	if b.store == "sqlite" {
		return ownsql.SQLite
	}

	return ownsql.Postgres
}

func (b *backing) env() []string {
	return []string{
		"BAKEOFF_ADAPTER=" + b.adapter, "BAKEOFF_STORE=" + b.store,
		"BAKEOFF_URL=" + b.url, "BAKEOFF_SCHEMA=" + b.schema, "BAKEOFF_PATH=" + b.path,
	}
}

func backingFromEnv() *backing {
	return &backing{
		adapter: os.Getenv("BAKEOFF_ADAPTER"), store: os.Getenv("BAKEOFF_STORE"),
		url: os.Getenv("BAKEOFF_URL"), schema: os.Getenv("BAKEOFF_SCHEMA"), path: os.Getenv("BAKEOFF_PATH"),
	}
}

func (b *backing) sqliteDSN() string {
	return "file:" + b.path + "?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_txlock=immediate"
}

func (b *backing) openSQL() (*sql.DB, error) {
	if b.store == "sqlite" {
		return sql.Open("sqlite", b.sqliteDSN()) //nolint:wrapcheck // Test.
	}

	return sql.Open("pgx", b.url) //nolint:wrapcheck // Test.
}

func q(b *backing, s string) string {
	if b.store == "sqlite" {
		return s
	}

	var sb strings.Builder

	n := 0

	for _, r := range s {
		if r == '?' {
			n++
			sb.WriteString("$" + strconv.Itoa(n))

			continue
		}

		sb.WriteRune(r)
	}

	return sb.String()
}

// newBacking makes a fresh database for one test, or skips.
func newBacking(t *testing.T, adapter, store string) *backing {
	t.Helper()

	b := &backing{adapter: adapter, store: store}

	if store == "postgres" {
		base := os.Getenv("DUSTPAN_TEST_POSTGRES_URL")
		if base == "" {
			t.Skip("set DUSTPAN_TEST_POSTGRES_URL")
		}

		suffix := make([]byte, 6)
		_, _ = rand.Read(suffix)
		b.schema = "bake_" + hex.EncodeToString(suffix)

		admin, err := sql.Open("pgx", base)
		if err != nil {
			t.Fatal(err)
		}

		if _, err := admin.ExecContext(t.Context(), "CREATE SCHEMA "+b.schema); err != nil {
			t.Fatal(err)
		}

		_ = admin.Close()

		u, _ := url.Parse(base)
		qv := u.Query()
		qv.Set("search_path", b.schema)
		u.RawQuery = qv.Encode()
		b.url = u.String()
	} else {
		b.path = filepath.Join(t.TempDir(), "bake.db")
	}

	db, err := b.openSQL()
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = db.Close() })
	b.db = db

	for _, s := range []string{
		`CREATE TABLE spike_domain (op TEXT NOT NULL, step TEXT NOT NULL, instance TEXT NOT NULL)`,
		`CREATE TABLE spike_fence (ns TEXT PRIMARY KEY, version BIGINT NOT NULL)`,
		`INSERT INTO spike_fence VALUES ('acme', 1)`,
	} {
		if _, err := db.ExecContext(t.Context(), s); err != nil {
			t.Fatal(err)
		}
	}

	if adapter == "own" {
		if err := ownsql.Migrate(t.Context(), db, b.dialect()); err != nil {
			t.Fatal(err)
		}
	}

	return b
}

// instanceOpts tunes one instance.
type instanceOpts struct {
	id         string
	executorID string // DBOS only; empty means id
	claimTTL   time.Duration
	renewEvery time.Duration
	sched      []durable.Schedule
}

// open opens one instance on b. The caller closes it.
func open(ctx context.Context, b *backing, o instanceOpts) (durable.Engine, error) {
	kinds := spikeKinds(o.id)

	if b.adapter == "own" {
		db, err := b.openSQL()
		if err != nil {
			return nil, err
		}

		e := ownsql.New(db, b.dialect(), o.id, kinds, o.sched, ownsql.Options{
			ClaimTTL: o.claimTTL, RenewEvery: o.renewEvery, Poll: 20 * time.Millisecond,
		})

		return closeBoth{Engine: e, db: db}, nil
	}

	exec := o.executorID
	if exec == "" {
		exec = o.id
	}

	cfg := dbosadapter.Config{ExecutorID: exec, AppVersion: "spike-1", Schema: "dbos_" + b.schema}

	if b.store == "postgres" {
		pool, err := pgxpool.New(ctx, b.url)
		if err != nil {
			return nil, err //nolint:wrapcheck // Test.
		}

		cfg.Pool = pool
	} else {
		db, err := b.openSQL()
		if err != nil {
			return nil, err
		}

		cfg.SQLite = db
	}

	return dbosadapter.New(ctx, cfg, kinds, o.sched)
}

type closeBoth struct {
	durable.Engine

	db *sql.DB
}

func (c closeBoth) Close(ctx context.Context) error {
	err := c.Engine.Close(ctx)
	_ = c.db.Close()

	return err
}

// Hooks that the helper process and the tests set. They are process-wide.
var (
	hookMu      sync.Mutex
	blockStep   = map[string]chan struct{}{} // step name -> released when closed
	stepStarted = map[string]chan struct{}{} // step name -> closed when it starts
)

func setBlock(step string) (release func(), started <-chan struct{}) {
	hookMu.Lock()
	defer hookMu.Unlock()

	ch := make(chan struct{})
	st := make(chan struct{})
	blockStep[step] = ch
	stepStarted[step] = st

	var once sync.Once

	return func() { once.Do(func() { close(ch) }) }, st
}

func waitHook(ctx context.Context, step string) {
	hookMu.Lock()
	ch, ok := blockStep[step]
	st := stepStarted[step]
	hookMu.Unlock()

	if !ok {
		return
	}

	select {
	case <-st:
	default:
		close(st)
	}

	select {
	case <-ch:
	case <-ctx.Done():
	}
}

// spikeKinds are the kinds every instance registers. instance names the
// instance in the domain rows.
func spikeKinds(instance string) []durable.Kind {
	domainStep := func(name string) durable.Step {
		return durable.Step{Name: name, Run: func(ctx context.Context, tx durable.Tx, input []byte) error {
			if _, err := tx.Exec(ctx, `INSERT INTO spike_domain (op, step, instance) VALUES (?, ?, ?)`,
				string(input), name, instance); err != nil {
				return err //nolint:wrapcheck // Test.
			}

			if name == "s2" {
				fmt.Fprintf(os.Stdout, "STEP-S2-STARTED\n")
			}

			waitHook(ctx, instance+"/"+name)

			return nil
		}}
	}

	fenced := durable.Step{Name: "fenced", Run: func(ctx context.Context, tx durable.Tx, input []byte) error {
		if _, err := tx.Exec(ctx, `INSERT INTO spike_domain (op, step, instance) VALUES (?, 'fenced', ?)`,
			string(input), instance); err != nil {
			return err //nolint:wrapcheck // Test.
		}

		var v int64
		if err := tx.QueryRow(ctx, `SELECT version FROM spike_fence WHERE ns = 'acme'`).Scan(&v); err != nil {
			return err //nolint:wrapcheck // Test.
		}

		if v != 1 {
			return fmt.Errorf("fenced: version %d, want 1", v)
		}

		return nil
	}}

	tick := durable.Step{Name: "tick", Run: func(ctx context.Context, tx durable.Tx, _ []byte) error {
		_, err := tx.Exec(ctx, `INSERT INTO spike_domain (op, step, instance) VALUES ('tick', 'tick', ?)`, instance)

		return err //nolint:wrapcheck // Test.
	}}

	return []durable.Kind{
		{Name: "three", Steps: []durable.Step{domainStep("s1"), domainStep("s2"), domainStep("s3")}},
		{Name: "fenced", Steps: []durable.Step{fenced}},
		{Name: "tick", Steps: []durable.Step{tick}},
	}
}

// count answers the domain rows of op and step.
func count(t *testing.T, b *backing, op, step string) int {
	t.Helper()

	var n int
	if err := b.db.QueryRowContext(t.Context(), q(b, `SELECT count(*) FROM spike_domain WHERE op = ? AND step = ?`), op, step).
		Scan(&n); err != nil {
		t.Fatal(err)
	}

	return n
}

// waitState polls e until op reaches want or the deadline passes, and
// answers the last status.
func waitState(t *testing.T, e durable.Engine, id string, want durable.State, d time.Duration) durable.Status {
	t.Helper()

	deadline := time.Now().Add(d)

	for {
		s, err := e.Get(t.Context(), id)
		if err == nil && s.State == want {
			return s
		}

		if time.Now().After(deadline) {
			return s
		}

		time.Sleep(50 * time.Millisecond)
	}
}

func strconvItoa(n int) string { return strconv.Itoa(n) }

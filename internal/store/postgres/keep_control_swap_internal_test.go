package postgres

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// insertDurableRow stores one running operation with key and token in the
// durable tables of schema, with one step.
func insertDurableRow(t *testing.T, pool *pgxpool.Pool, schema, key string, token int64) {
	t.Helper()

	ctx := t.Context()

	ident := pgx.Identifier{schema}.Sanitize()

	insertOp := "INSERT INTO " + ident + ".durable_operations (id, start_key, kind, input, version, state, attempt," +
		" last_error, claim_owner, claim_token, claim_expires_at_ms, cancel_requested, not_before_ms, created_at_ms," +
		" updated_at_ms) VALUES ($1, $2, 'kind-a', '', 0, 'running', $3, '', 'instance-a', $3, 9999999999999," +
		" false, 0, 0, 0)"
	if _, err := pool.Exec(ctx, insertOp, "op-"+key, key, token); err != nil {
		t.Fatalf("insert operation %q into %s: %v", key, schema, err)
	}

	insertStep := "INSERT INTO " + ident + ".durable_steps (operation_id, name, seq, result, done_at_ms)" +
		" VALUES ($1, 'one', 1, '', 0)"
	if _, err := pool.Exec(ctx, insertStep, "op-"+key); err != nil {
		t.Fatalf("insert step of %q into %s: %v", key, schema, err)
	}
}

// liveSchema returns the schema that the store serves.
func liveSchema(ctx context.Context, t *testing.T, s *Store) string {
	t.Helper()

	var live string
	if err := s.pool.QueryRow(ctx, "SELECT current_schema()").Scan(&live); err != nil {
		t.Fatalf("read live schema: %v", err)
	}

	return live
}

// durableTokens returns the claim token of each operation of schema, by key.
func durableTokens(ctx context.Context, t *testing.T, pool *pgxpool.Pool, schema string) map[string]int64 {
	t.Helper()

	rows, err := pool.Query(ctx, "SELECT start_key, claim_token FROM "+pgx.Identifier{schema}.Sanitize()+
		".durable_operations")
	if err != nil {
		t.Fatalf("read durable operations of %s: %v", schema, err)
	}

	defer rows.Close()

	out := map[string]int64{}

	for rows.Next() {
		var (
			key   string
			token int64
		)

		if scanErr := rows.Scan(&key, &token); scanErr != nil {
			t.Fatalf("read durable operation: %v", scanErr)
		}

		out[key] = token
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		t.Fatalf("read durable operations of %s: %v", schema, rowsErr)
	}

	return out
}

// stageWithDurableRows opens a store, stages a portable export of it, and
// stores the live operation "live" at token 5 and the staged operation
// "from-backup" at token 3. It returns the store and its live schema.
func stageWithDurableRows(t *testing.T) (*Store, string) {
	t.Helper()

	ctx := t.Context()

	s := openKeepLockStore(t, testPostgresURL(t))
	sink := &keepLockSink{files: map[string]*bytes.Buffer{}}

	sum, files, err := s.ExportPortable(ctx, sink.open)
	if err != nil {
		t.Fatalf("export portable: %v", err)
	}

	staged, err := s.StagePortable(ctx, sink.read, sum, files, storetest.StageAs(t, s))
	if err != nil {
		t.Fatalf("stage portable: %v", err)
	}

	if staged.Dialect != sum.Dialect {
		t.Fatalf("staged dialect = %q, want the exported %q", staged.Dialect, sum.Dialect)
	}

	live := liveSchema(ctx, t, s)
	insertDurableRow(t, s.pool, live, "live", 5)
	insertDurableRow(t, s.pool, s.stagingSchema, "from-backup", 3)

	return s, live
}

// checkKeptLiveRow fails the test unless schema holds the live operation at
// token 5, plus the keys in extra, and no other operation.
func checkKeptLiveRow(ctx context.Context, t *testing.T, s *Store, schema string, extra ...string) {
	t.Helper()

	got := durableTokens(ctx, t, s.pool, schema)

	if got["live"] != 5 || len(got) != 1+len(extra) {
		t.Fatalf("durable operations after the restore = %v, want live at token 5 and %v", got, extra)
	}

	for _, key := range extra {
		if _, ok := got[key]; !ok {
			t.Fatalf("durable operations after the restore = %v, want %q too", got, key)
		}
	}
}

// TestApplyStagingKeepsDurableRows proves the swap of a restore keeps the
// durable operations of the live schema, with their claim tokens, and brings
// back none from the staged backup.
func TestApplyStagingKeepsDurableRows(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s, live := stageWithDurableRows(t)

	if err := s.ApplyStaging(ctx); err != nil {
		t.Fatalf("apply staging: %v", err)
	}

	checkKeptLiveRow(ctx, t, s, live)
}

// TestApplyStagingKeepingAccessKeepsDurableRows proves the same for the
// restore that keeps the access tables, which swaps through its own path.
func TestApplyStagingKeepingAccessKeepsDurableRows(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s, live := stageWithDurableRows(t)

	if _, err := s.ApplyStagingKeepingAccess(ctx); err != nil {
		t.Fatalf("apply staging keeping the access tables: %v", err)
	}

	checkKeptLiveRow(ctx, t, s, live)
}

// lockWait is how long the lock test waits to see that a write blocks.
const lockWait = 500 * time.Millisecond

// TestApplyStagingHoldsDurableWrites proves the swap locks the live durable
// tables from the copy to the commit. A third connection holds a table of
// the live schema, so the swap stops at the drop of the old schema, after
// the copy. An insert into durable_operations on a second connection then
// waits for the swap, and after the commit it lands in the restored schema.
// Without the lock the insert goes into the old table before the commit and
// is lost with it.
func TestApplyStagingHoldsDurableWrites(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s, live := stageWithDurableRows(t)
	ident := pgx.Identifier{live}.Sanitize()

	holder, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin holder: %v", err)
	}

	defer func() {
		_ = holder.Rollback(context.WithoutCancel(ctx)) //nolint:errcheck // The holder commits on the main path.
	}()

	if _, lockErr := holder.Exec(ctx, "LOCK TABLE "+ident+".stacks IN ACCESS SHARE MODE"); lockErr != nil {
		t.Fatalf("hold a table of the live schema: %v", lockErr)
	}

	applied := make(chan error, 1)

	go func() { applied <- s.ApplyStaging(ctx) }()

	waitForDropSchema(ctx, t, s.pool)

	inserted := make(chan error, 1)

	go func() {
		insert := "INSERT INTO " + ident + ".durable_operations (id, start_key, kind, input, version, state," +
			" attempt, last_error, claim_owner, claim_token, claim_expires_at_ms, cancel_requested, not_before_ms," +
			" created_at_ms, updated_at_ms) VALUES ('op-during', 'during', 'kind-a', '', 0, 'pending', 0, '', NULL," +
			" 0, NULL, false, 0, 0, 0)"
		_, insertErr := s.pool.Exec(ctx, insert)
		inserted <- insertErr
	}()

	select {
	case insertErr := <-inserted:
		t.Fatalf("insert during the swap ended before the commit (error %v), want it to wait for the lock", insertErr)
	case <-time.After(lockWait):
	}

	if commitErr := holder.Commit(ctx); commitErr != nil {
		t.Fatalf("release the held table: %v", commitErr)
	}

	if applyErr := <-applied; applyErr != nil {
		t.Fatalf("apply staging: %v", applyErr)
	}

	if insertErr := <-inserted; insertErr != nil {
		t.Fatalf("insert after the swap: %v", insertErr)
	}

	checkKeptLiveRow(ctx, t, s, live, "during")
}

// errNoDropWait reports a swap that never reached the drop of the old schema.
var errNoDropWait = errors.New("the swap did not wait at the drop of the old schema")

// waitForDropSchema waits until a session of the database of pool waits for a
// lock in a DROP SCHEMA statement: the swap holds its copy and its locks
// there. pg_stat_activity shows the sessions of every database on the server,
// so the query reads only the sessions of this database.
func waitForDropSchema(ctx context.Context, t *testing.T, pool *pgxpool.Pool) {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)

	for time.Now().Before(deadline) {
		var waiting int

		query := "SELECT count(*) FROM pg_stat_activity WHERE wait_event_type = 'Lock'" +
			" AND datname = current_database() AND query LIKE 'DROP SCHEMA%'"
		if err := pool.QueryRow(ctx, query).Scan(&waiting); err != nil {
			t.Fatalf("read waiting sessions: %v", err)
		}

		if waiting > 0 {
			return
		}

		select {
		case <-time.After(20 * time.Millisecond):
		case <-ctx.Done():
			t.Fatalf("wait for the swap: %v", ctx.Err())
		}
	}

	t.Fatal(errNoDropWait)
}

package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// schemaRandBytes is the random part of the schema name of one run.
const schemaRandBytes = 8

// benchNamespace is the namespace row that the fenced writes check.
const benchNamespace = "bench"

// maxPoolConns bounds the connections of the model pool. It is above the
// highest client concurrency of the protocol run, so no worker waits for a
// connection.
const maxPoolConns = 40

// errFenceMoved reports a fence row that holds an unexpected version.
var errFenceMoved = errors.New("fence version moved")

// pgModel holds the model tables of the database-arbitrated writes: a fence
// row for each namespace, as the namespace table holds, and one row for each
// stored blob that names its key, as the checkpoint and state tables do. The
// pool serves concurrent workers.
type pgModel struct {
	pool   *pgxpool.Pool
	schema string
}

// openPGModel creates the model tables in a fresh schema of the database at
// dsn, so runs never share rows, and opens a pool on that schema.
func openPGModel(ctx context.Context, dsn string) (*pgModel, error) {
	raw := make([]byte, schemaRandBytes)
	if _, err := rand.Read(raw); err != nil {
		return nil, fmt.Errorf("random schema name: %w", err)
	}

	schema := "bench_" + hex.EncodeToString(raw)

	if err := createModelSchema(ctx, dsn, schema); err != nil {
		return nil, err
	}

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse the bench database URL: %w", err)
	}

	cfg.MaxConns = maxPoolConns
	cfg.ConnConfig.RuntimeParams["search_path"] = schema

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("open the bench database pool: %w", err)
	}

	return &pgModel{pool: pool, schema: schema}, nil
}

// createModelSchema creates schema with the model tables and the fence row.
func createModelSchema(ctx context.Context, dsn, schema string) error {
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return fmt.Errorf("connect to the bench database: %w", err)
	}

	quoted := pgx.Identifier{schema}.Sanitize()

	for _, stmt := range []string{
		"CREATE SCHEMA " + quoted,
		"SET search_path TO " + quoted,
		"CREATE TABLE fence (ns TEXT PRIMARY KEY, version BIGINT NOT NULL)",
		"CREATE TABLE blob_rows (unit TEXT NOT NULL, seq BIGINT NOT NULL, blob_key TEXT NOT NULL, " +
			"fence BIGINT NOT NULL, PRIMARY KEY (unit, seq))",
		"CREATE TABLE audit_chain (ns TEXT PRIMARY KEY, seq BIGINT NOT NULL)",
		"CREATE TABLE audit_entries (ns TEXT NOT NULL, seq BIGINT NOT NULL, object TEXT NOT NULL, " +
			"PRIMARY KEY (ns, seq))",
	} {
		if _, execErr := conn.Exec(ctx, stmt); execErr != nil {
			return errors.Join(fmt.Errorf("set up the model tables: %w", execErr), conn.Close(ctx))
		}
	}

	_, err = conn.Exec(ctx, "INSERT INTO fence (ns, version) VALUES ($1, 1)", benchNamespace)
	if err != nil {
		return errors.Join(fmt.Errorf("set up the fence row: %w", err), conn.Close(ctx))
	}

	_, err = conn.Exec(ctx, "INSERT INTO audit_chain (ns, seq) VALUES ($1, 0)", benchNamespace)
	if err != nil {
		return errors.Join(fmt.Errorf("set up the audit chain row: %w", err), conn.Close(ctx))
	}

	if err := conn.Close(ctx); err != nil {
		return fmt.Errorf("close the setup connection: %w", err)
	}

	return nil
}

// close drops the schema of the run and closes the pool.
func (m *pgModel) close(ctx context.Context) error {
	_, dropErr := m.pool.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{m.schema}.Sanitize()+" CASCADE")

	m.pool.Close()

	if dropErr != nil {
		return fmt.Errorf("drop the model schema: %w", dropErr)
	}

	return nil
}

// truncate deletes every blob row and audit entry, between two runs.
func (m *pgModel) truncate(ctx context.Context) error {
	if _, err := m.pool.Exec(ctx, "TRUNCATE blob_rows, audit_entries"); err != nil {
		return fmt.Errorf("truncate the blob rows: %w", err)
	}

	if _, err := m.pool.Exec(ctx, "UPDATE audit_chain SET seq = 0"); err != nil {
		return fmt.Errorf("reset the audit chain: %w", err)
	}

	return nil
}

// commitAudited is the fenced, audited commit transaction of an upload: the
// start, the read of the fence row with a lock for update, the insert of the
// commit row, four statements of the audit append (the chain record, the
// last entry, the insert of the entry, the update of the chain record), and
// the commit. It is eight round trips. The lock on the fence row orders the
// commits of one namespace, as the store's own audited writes do.
func (m *pgModel) commitAudited(ctx context.Context, unit string, seq int64, key string) error {
	tx, err := m.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}

	var version, chain, last int64

	steps := []func() error{
		func() error {
			return tx.QueryRow(ctx, "SELECT version FROM fence WHERE ns = $1 FOR UPDATE", benchNamespace).Scan(&version)
		},
		func() error {
			return execIn(ctx, tx, "INSERT INTO blob_rows (unit, seq, blob_key, fence) "+
				"SELECT $1, $2, $3, version FROM fence WHERE ns = $4 AND version = 1", unit, seq, key, benchNamespace)
		},
		func() error {
			return tx.QueryRow(ctx, "SELECT seq FROM audit_chain WHERE ns = $1", benchNamespace).Scan(&chain)
		},
		func() error {
			return tx.QueryRow(ctx, "SELECT COALESCE(MAX(seq), 0) FROM audit_entries WHERE ns = $1", benchNamespace).
				Scan(&last)
		},
		func() error {
			return execIn(ctx, tx, "INSERT INTO audit_entries (ns, seq, object) VALUES ($1, $2, $3)",
				benchNamespace, last+1, key)
		},
		func() error {
			return execIn(ctx, tx, "UPDATE audit_chain SET seq = $2 WHERE ns = $1", benchNamespace, last+1)
		},
	}

	for _, step := range steps {
		if stepErr := step(); stepErr != nil {
			return errors.Join(fmt.Errorf("audited commit: %w", stepErr), tx.Rollback(ctx))
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	return nil
}

// execIn runs one statement in tx.
func execIn(ctx context.Context, tx pgx.Tx, sql string, args ...any) error {
	if _, err := tx.Exec(ctx, sql, args...); err != nil {
		return fmt.Errorf("exec: %w", err)
	}

	return nil
}

// readAuditChain is one read of the audit chain row: the model of a read of
// the last entry of a scope, or of the state of the check marks.
func (m *pgModel) readAuditChain(ctx context.Context) error {
	var seq int64

	if err := m.pool.QueryRow(ctx, "SELECT seq FROM audit_chain WHERE ns = $1", benchNamespace).Scan(&seq); err != nil {
		return fmt.Errorf("read the audit chain: %w", err)
	}

	return nil
}

// fencedWrite is the fenced transaction of a blob write: begin, read the
// fence row with a share lock, insert the row that names the blob key, and
// commit. It is four round trips on a warm connection, as the store's own
// fenced writes are.
func (m *pgModel) fencedWrite(ctx context.Context, unit string, seq int64, key string) error {
	tx, err := m.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}

	var version int64

	err = tx.QueryRow(ctx, "SELECT version FROM fence WHERE ns = $1 FOR SHARE", benchNamespace).Scan(&version)
	if err != nil {
		return errors.Join(fmt.Errorf("read fence: %w", err), tx.Rollback(ctx))
	}

	if version != 1 {
		return errors.Join(fmt.Errorf("%w: %d", errFenceMoved, version), tx.Rollback(ctx))
	}

	_, err = tx.Exec(ctx, "INSERT INTO blob_rows (unit, seq, blob_key, fence) VALUES ($1, $2, $3, $4)",
		unit, seq, key, version)
	if err != nil {
		return errors.Join(fmt.Errorf("insert row: %w", err), tx.Rollback(ctx))
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	return nil
}

// checkFence reads the fence row: one round trip, as a writer checks the
// fence again before a write.
func (m *pgModel) checkFence(ctx context.Context) error {
	var version int64

	err := m.pool.QueryRow(ctx, "SELECT version FROM fence WHERE ns = $1", benchNamespace).Scan(&version)
	if err != nil {
		return fmt.Errorf("check fence: %w", err)
	}

	if version != 1 {
		return fmt.Errorf("%w: %d", errFenceMoved, version)
	}

	return nil
}

// readRow is one extra round trip: the read of the row that decided which
// write won.
func (m *pgModel) readRow(ctx context.Context, unit string, seq int64) error {
	var key string

	err := m.pool.QueryRow(ctx, "SELECT blob_key FROM blob_rows WHERE unit = $1 AND seq = $2", unit, seq).Scan(&key)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("read row: %w", err)
	}

	return nil
}

// referencedKeys reads the keys that the rows of unit name: the keep set of
// an orphan sweep.
func (m *pgModel) referencedKeys(ctx context.Context, unit string) (map[string]struct{}, error) {
	rows, err := m.pool.Query(ctx, "SELECT blob_key FROM blob_rows WHERE unit = $1", unit)
	if err != nil {
		return nil, fmt.Errorf("read referenced keys: %w", err)
	}

	keys, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("read referenced keys: %w", err)
	}

	out := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		out[k] = struct{}{}
	}

	return out, nil
}

// ping is one round trip with no table read.
func (m *pgModel) ping(ctx context.Context) error {
	var one int

	if err := m.pool.QueryRow(ctx, "SELECT 1").Scan(&one); err != nil {
		return fmt.Errorf("ping: %w", err)
	}

	return nil
}

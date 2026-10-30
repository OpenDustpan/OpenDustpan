package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// schemaRandBytes is the random part of the schema name of one run.
const schemaRandBytes = 8

// benchNamespace is the namespace row that the fenced writes check.
const benchNamespace = "bench"

// errFenceMoved reports a fence row that holds an unexpected version.
var errFenceMoved = errors.New("fence version moved")

// pgModel holds the model tables of the database-arbitrated writes: a fence
// row for each namespace, as the namespace table holds, and one row for each
// stored blob that names its key, as the checkpoint and state tables do.
type pgModel struct {
	conn   *pgx.Conn
	schema string
}

// openPGModel connects to dsn and creates the model tables in a fresh
// schema, so runs never share rows.
func openPGModel(ctx context.Context, dsn string) (*pgModel, error) {
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("connect to the bench database: %w", err)
	}

	raw := make([]byte, schemaRandBytes)
	if _, err := rand.Read(raw); err != nil {
		return nil, errors.Join(fmt.Errorf("random schema name: %w", err), conn.Close(ctx))
	}

	m := &pgModel{conn: conn, schema: "bench_" + hex.EncodeToString(raw)}
	schema := pgx.Identifier{m.schema}.Sanitize()

	for _, stmt := range []string{
		"CREATE SCHEMA " + schema,
		"SET search_path TO " + schema,
		"CREATE TABLE fence (ns TEXT PRIMARY KEY, version BIGINT NOT NULL)",
		"CREATE TABLE blob_rows (unit TEXT NOT NULL, seq BIGINT NOT NULL, blob_key TEXT NOT NULL, " +
			"fence BIGINT NOT NULL, PRIMARY KEY (unit, seq))",
	} {
		if _, err := conn.Exec(ctx, stmt); err != nil {
			return nil, errors.Join(fmt.Errorf("set up the model tables: %w", err), conn.Close(ctx))
		}
	}

	if _, err := conn.Exec(ctx, "INSERT INTO fence (ns, version) VALUES ($1, 1)", benchNamespace); err != nil {
		return nil, errors.Join(fmt.Errorf("set up the fence row: %w", err), conn.Close(ctx))
	}

	return m, nil
}

// close drops the schema of the run and closes the connection.
func (m *pgModel) close(ctx context.Context) error {
	_, dropErr := m.conn.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{m.schema}.Sanitize()+" CASCADE")

	return errors.Join(dropErr, m.conn.Close(ctx))
}

// fencedWrite is the fenced transaction of a blob write: begin, read the
// fence row with a share lock, insert the row that names the blob key, and
// commit. It is four round trips on a warm connection, as the store's own
// fenced writes are.
func (m *pgModel) fencedWrite(ctx context.Context, unit string, seq int64, key string) error {
	tx, err := m.conn.Begin(ctx)
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

// readRow is one extra round trip: the read of the row that decided which
// write won.
func (m *pgModel) readRow(ctx context.Context, unit string, seq int64) error {
	var key string

	err := m.conn.QueryRow(ctx, "SELECT blob_key FROM blob_rows WHERE unit = $1 AND seq = $2", unit, seq).Scan(&key)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("read row: %w", err)
	}

	return nil
}

// referencedKeys reads the keys that the rows of unit name: the keep set of
// an orphan sweep.
func (m *pgModel) referencedKeys(ctx context.Context, unit string) (map[string]struct{}, error) {
	rows, err := m.conn.Query(ctx, "SELECT blob_key FROM blob_rows WHERE unit = $1", unit)
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

	if err := m.conn.QueryRow(ctx, "SELECT 1").Scan(&one); err != nil {
		return fmt.Errorf("ping: %w", err)
	}

	return nil
}

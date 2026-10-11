// Package sqlite is the SQLite store adapter: the zero-dependency default for
// single-node installs. It uses a pure-Go SQLite build, so binaries need no cgo.
//
// SQLite allows one writer at a time. Transactions start as IMMEDIATE so a
// fenced write takes the write lock before it reads the failover version.
package sqlite

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"

	// Registers the "sqlite3" database/sql driver.
	_ "github.com/ncruces/go-sqlite3/driver"

	"opendustpan.org/opendustpan/namespace"
	"opendustpan.org/opendustpan/stack"
	"opendustpan.org/opendustpan/store/internal/codec"
	"opendustpan.org/opendustpan/store/internal/migrate"
	"opendustpan.org/opendustpan/store/sqlite/internal/db"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Store implements namespace.Store, stack.Store and the update-cycle ports
// (stack.UpdateStore, stack.CheckpointStore, stack.EventStore) on SQLite.
type Store struct {
	conn    *sql.DB
	queries *db.Queries
}

var (
	_ namespace.Store = (*Store)(nil)
	_ stack.Store     = (*Store)(nil)
)

// Open opens (creating if needed) the database file at path and migrates it.
func Open(ctx context.Context, path string) (*Store, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve database path: %w", err)
	}

	query := url.Values{}
	query.Set("_txlock", "immediate")
	query.Add("_pragma", "busy_timeout(10000)")
	query.Add("_pragma", "journal_mode(wal)")
	query.Add("_pragma", "synchronous(normal)")
	query.Add("_pragma", "foreign_keys(on)")

	dsn := (&url.URL{Scheme: "file", Path: abs, RawQuery: query.Encode()}).String()

	conn, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite database: %w", err)
	}

	if err := conn.PingContext(ctx); err != nil {
		return nil, errors.Join(fmt.Errorf("connect to sqlite database: %w", err), conn.Close())
	}

	s := &Store{conn: conn, queries: db.New(conn)}
	if err := s.migrate(ctx); err != nil {
		return nil, errors.Join(err, conn.Close())
	}

	return s, nil
}

// Close closes the database.
func (s *Store) Close() error {
	if err := s.conn.Close(); err != nil {
		return fmt.Errorf("close sqlite database: %w", err)
	}

	return nil
}

// CreateNamespace implements namespace.Store.
func (s *Store) CreateNamespace(ctx context.Context, ns namespace.Namespace) error {
	row := codec.ToNamespaceRow(ns)

	n, err := s.queries.CreateNamespace(ctx, db.CreateNamespaceParams{
		Name:            row.Name,
		HomeRegion:      row.HomeRegion,
		FailoverVersion: row.FailoverVersion,
		CreatedAtMs:     row.CreatedAtMs,
	})
	if err != nil {
		return fmt.Errorf("insert namespace: %w", err)
	}

	if n == 0 {
		return namespace.ErrAlreadyExists
	}

	return nil
}

// GetNamespace implements namespace.Store.
func (s *Store) GetNamespace(ctx context.Context, name namespace.Name) (namespace.Namespace, error) {
	row, err := s.queries.GetNamespace(ctx, name.String())
	if errors.Is(err, sql.ErrNoRows) {
		return namespace.Namespace{}, namespace.ErrNotFound
	}

	if err != nil {
		return namespace.Namespace{}, fmt.Errorf("select namespace: %w", err)
	}

	return codec.FromNamespaceRow(codec.NamespaceRow(row)), nil
}

// CreateStack implements stack.Store with a fenced insert.
func (s *Store) CreateStack(ctx context.Context, st stack.Stack, fence namespace.Fence) error {
	row, err := codec.ToStackRow(st)
	if err != nil {
		return err
	}

	return s.inFencedTx(ctx, fence, func(q *db.Queries) error {
		n, err := q.CreateStack(ctx, db.CreateStackParams{
			Namespace:   row.Namespace,
			Project:     row.Project,
			Name:        row.Name,
			ID:          row.ID,
			TagsJson:    string(row.Tags),
			Version:     row.Version,
			CreatedAtMs: row.CreatedAtMs,
			UpdatedAtMs: row.UpdatedAtMs,
		})
		if err != nil {
			return fmt.Errorf("insert stack: %w", err)
		}

		if n == 0 {
			return stack.ErrAlreadyExists
		}

		return nil
	})
}

// GetStack implements stack.Store.
func (s *Store) GetStack(ctx context.Context, ref stack.Ref) (stack.Stack, error) {
	row, err := s.queries.GetStack(ctx, db.GetStackParams{
		Namespace: ref.Namespace.String(),
		Project:   ref.Project.String(),
		Name:      ref.Stack.String(),
	})
	if errors.Is(err, sql.ErrNoRows) {
		return stack.Stack{}, stack.ErrNotFound
	}

	if err != nil {
		return stack.Stack{}, fmt.Errorf("select stack: %w", err)
	}

	return codec.FromStackRow(codec.StackRow{
		Namespace:   row.Namespace,
		Project:     row.Project,
		Name:        row.Name,
		ID:          row.ID,
		Tags:        []byte(row.TagsJson),
		Version:     row.Version,
		CreatedAtMs: row.CreatedAtMs,
		UpdatedAtMs: row.UpdatedAtMs,
	})
}

// ListStacks implements stack.Store.
func (s *Store) ListStacks(ctx context.Context, filter stack.Filter) ([]stack.Stack, error) {
	if filter.Project == "" {
		rows, err := s.queries.ListStacksInNamespace(ctx, filter.Namespace.String())
		if err != nil {
			return nil, fmt.Errorf("select stacks: %w", err)
		}

		converted := make([]codec.StackRow, 0, len(rows))
		for _, row := range rows {
			converted = append(converted, codec.StackRow{
				Namespace:   row.Namespace,
				Project:     row.Project,
				Name:        row.Name,
				ID:          row.ID,
				Tags:        []byte(row.TagsJson),
				Version:     row.Version,
				CreatedAtMs: row.CreatedAtMs,
				UpdatedAtMs: row.UpdatedAtMs,
			})
		}

		return codec.FromStackRows(converted)
	}

	rows, err := s.queries.ListStacksInProject(ctx, db.ListStacksInProjectParams{
		Namespace: filter.Namespace.String(),
		Project:   filter.Project.String(),
	})
	if err != nil {
		return nil, fmt.Errorf("select stacks: %w", err)
	}

	converted := make([]codec.StackRow, 0, len(rows))
	for _, row := range rows {
		converted = append(converted, codec.StackRow{
			Namespace:   row.Namespace,
			Project:     row.Project,
			Name:        row.Name,
			ID:          row.ID,
			Tags:        []byte(row.TagsJson),
			Version:     row.Version,
			CreatedAtMs: row.CreatedAtMs,
			UpdatedAtMs: row.UpdatedAtMs,
		})
	}

	return codec.FromStackRows(converted)
}

func (s *Store) inTx(ctx context.Context, fn func(q *db.Queries) error) error {
	tx, err := s.conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}

	if err := fn(s.queries.WithTx(tx)); err != nil {
		return errors.Join(err, rollback(tx))
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}

	return nil
}

// inFencedTx runs fn in a transaction that first checks fence. The write lock
// is held from the start, so the failover version cannot change before commit.
func (s *Store) inFencedTx(ctx context.Context, fence namespace.Fence, fn func(q *db.Queries) error) error {
	return s.inTx(ctx, func(q *db.Queries) error {
		version, err := q.GetFailoverVersion(ctx, fence.Namespace.String())
		if errors.Is(err, sql.ErrNoRows) {
			return namespace.ErrNotFound
		}

		if err != nil {
			return fmt.Errorf("read failover version: %w", err)
		}

		if version != fence.FailoverVersion {
			return namespace.ErrFenced
		}

		return fn(q)
	})
}

func (s *Store) migrate(ctx context.Context) error {
	all, err := migrate.Load(migrations, "migrations")
	if err != nil {
		return err
	}

	const createTable = `CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER NOT NULL PRIMARY KEY,
		name    TEXT    NOT NULL
	) STRICT`

	if _, err := s.conn.ExecContext(ctx, createTable); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	for _, m := range all {
		if err := s.apply(ctx, m); err != nil {
			return err
		}
	}

	return nil
}

func (s *Store) apply(ctx context.Context, m migrate.Migration) error {
	tx, err := s.conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration %s: %w", m.Name, err)
	}

	var applied int64

	err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, m.Version).Scan(&applied)
	if err != nil {
		return errors.Join(fmt.Errorf("check migration %s: %w", m.Name, err), rollback(tx))
	}

	if applied > 0 {
		return rollback(tx)
	}

	if _, err := tx.ExecContext(ctx, m.SQL); err != nil {
		return errors.Join(fmt.Errorf("apply migration %s: %w", m.Name, err), rollback(tx))
	}

	const record = `INSERT INTO schema_migrations (version, name) VALUES (?, ?)`
	if _, err := tx.ExecContext(ctx, record, m.Version, m.Name); err != nil {
		return errors.Join(fmt.Errorf("record migration %s: %w", m.Name, err), rollback(tx))
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration %s: %w", m.Name, err)
	}

	return nil
}

func rollback(tx *sql.Tx) error {
	if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
		return fmt.Errorf("roll back transaction: %w", err)
	}

	return nil
}

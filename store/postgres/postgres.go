// Package postgres is the PostgreSQL store adapter, built on pgx.
//
// Fenced writes lock the namespace row with FOR SHARE inside the write
// transaction, so a concurrent promotion (which updates that row) waits until
// the write commits or aborts. Migrations run under a transaction-scoped
// advisory lock, so several servers can start at once.
package postgres

import (
	"context"
	"embed"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"opendustpan.org/opendustpan/namespace"
	"opendustpan.org/opendustpan/stack"
	"opendustpan.org/opendustpan/store/internal/codec"
	"opendustpan.org/opendustpan/store/internal/migrate"
	"opendustpan.org/opendustpan/store/postgres/internal/db"
)

//go:embed migrations/*.sql
var migrations embed.FS

// migrationLockID is the advisory lock key held while migrating ("odpn" in ASCII).
const migrationLockID int64 = 0x6f64706e

// Store implements namespace.Store, stack.Store and the update-cycle ports
// (stack.UpdateStore, stack.CheckpointStore, stack.EventStore) on PostgreSQL.
type Store struct {
	pool    *pgxpool.Pool
	queries *db.Queries
}

var (
	_ namespace.Store = (*Store)(nil)
	_ stack.Store     = (*Store)(nil)
)

// Open connects to the database at url and migrates it.
func Open(ctx context.Context, url string) (*Store, error) {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("open postgres pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()

		return nil, fmt.Errorf("connect to postgres: %w", err)
	}

	s := &Store{pool: pool, queries: db.New(pool)}
	if err := s.migrate(ctx); err != nil {
		pool.Close()

		return nil, err
	}

	return s, nil
}

// Close closes every connection in the pool.
func (s *Store) Close() {
	s.pool.Close()
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
	if errors.Is(err, pgx.ErrNoRows) {
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
		n, err := q.CreateStack(ctx, db.CreateStackParams(row))
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
	if errors.Is(err, pgx.ErrNoRows) {
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
		Tags:        row.Tags,
		Version:     row.Version,
		CreatedAtMs: row.CreatedAtMs,
		UpdatedAtMs: row.UpdatedAtMs,
	})
}

// RenameStack implements stack.Store. It renames the stack without moving its
// history, checkpoint or events: child rows key on the stable stack ID.
func (s *Store) RenameStack(
	ctx context.Context, oldRef stack.Ref, newName stack.Name, fence namespace.Fence,
) error {
	return s.inFencedTx(ctx, fence, func(q *db.Queries) error {
		if newName == oldRef.Stack {
			return confirmStackPresent(ctx, q, oldRef)
		}

		if err := rejectTakenName(ctx, q, oldRef, newName); err != nil {
			return err
		}

		n, err := q.RenameStack(ctx, db.RenameStackParams{
			Name:      newName.String(),
			Namespace: oldRef.Namespace.String(),
			Project:   oldRef.Project.String(),
			Name_2:    oldRef.Stack.String(),
		})
		if err != nil {
			return fmt.Errorf("rename stack: %w", err)
		}

		if n == 0 {
			return stack.ErrNotFound
		}

		return nil
	})
}

// confirmStackPresent returns nil when ref exists and ErrNotFound otherwise.
// It answers a rename that keeps the current name.
func confirmStackPresent(ctx context.Context, q *db.Queries, ref stack.Ref) error {
	params := db.GetStackParams{
		Namespace: ref.Namespace.String(),
		Project:   ref.Project.String(),
		Name:      ref.Stack.String(),
	}

	_, err := q.GetStack(ctx, params)
	if errors.Is(err, pgx.ErrNoRows) {
		return stack.ErrNotFound
	}

	if err != nil {
		return fmt.Errorf("read stack: %w", err)
	}

	return nil
}

// rejectTakenName returns ErrAlreadyExists when newName already names a stack
// of the same project.
func rejectTakenName(ctx context.Context, q *db.Queries, oldRef stack.Ref, newName stack.Name) error {
	params := db.GetStackParams{
		Namespace: oldRef.Namespace.String(),
		Project:   oldRef.Project.String(),
		Name:      newName.String(),
	}

	_, err := q.GetStack(ctx, params)
	if err == nil {
		return stack.ErrAlreadyExists
	}

	if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("check new stack name: %w", err)
	}

	return nil
}

// DeleteStackForTest deletes a stack and, through foreign keys, its updates,
// checkpoints and events. It is test-only until issue #49 adds the delete port.
func (s *Store) DeleteStackForTest(ctx context.Context, ref stack.Ref, fence namespace.Fence) error {
	return s.inFencedTx(ctx, fence, func(q *db.Queries) error {
		err := q.DeleteStack(ctx, db.DeleteStackParams{
			Namespace: ref.Namespace.String(),
			Project:   ref.Project.String(),
			Name:      ref.Stack.String(),
		})
		if err != nil {
			return fmt.Errorf("delete stack: %w", err)
		}

		return nil
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
				Tags:        row.Tags,
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
			Tags:        row.Tags,
			Version:     row.Version,
			CreatedAtMs: row.CreatedAtMs,
			UpdatedAtMs: row.UpdatedAtMs,
		})
	}

	return codec.FromStackRows(converted)
}

func (s *Store) inTx(ctx context.Context, fn func(q *db.Queries) error) error {
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		return fn(s.queries.WithTx(tx))
	})
	if err != nil {
		return fmt.Errorf("transaction: %w", err)
	}

	return nil
}

// inFencedTx runs fn in a transaction that first checks fence, holding the
// namespace row FOR SHARE so a promotion waits until the transaction ends.
func (s *Store) inFencedTx(ctx context.Context, fence namespace.Fence, fn func(q *db.Queries) error) error {
	return s.inTx(ctx, func(q *db.Queries) error {
		version, err := q.GetFailoverVersionForShare(ctx, fence.Namespace.String())
		if errors.Is(err, pgx.ErrNoRows) {
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

	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		return migrateTx(ctx, tx, all)
	})
	if err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	return nil
}

// migrateTx applies pending migrations while holding the advisory lock.
func migrateTx(ctx context.Context, tx pgx.Tx, all []migrate.Migration) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, migrationLockID); err != nil {
		return fmt.Errorf("take migration lock: %w", err)
	}

	const createTable = `CREATE TABLE IF NOT EXISTS schema_migrations (
		version BIGINT NOT NULL PRIMARY KEY,
		name    TEXT   NOT NULL
	)`

	if _, err := tx.Exec(ctx, createTable); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	for _, m := range all {
		if err := apply(ctx, tx, m); err != nil {
			return err
		}
	}

	return nil
}

func apply(ctx context.Context, tx pgx.Tx, m migrate.Migration) error {
	var applied bool

	err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = $1)`, m.Version).
		Scan(&applied)
	if err != nil {
		return fmt.Errorf("check migration %s: %w", m.Name, err)
	}

	if applied {
		return nil
	}

	// No arguments, so pgx uses the simple protocol, which allows several statements.
	if _, err := tx.Exec(ctx, m.SQL); err != nil {
		return fmt.Errorf("apply migration %s: %w", m.Name, err)
	}

	const record = `INSERT INTO schema_migrations (version, name) VALUES ($1, $2)`
	if _, err := tx.Exec(ctx, record, m.Version, m.Name); err != nil {
		return fmt.Errorf("record migration %s: %w", m.Name, err)
	}

	return nil
}

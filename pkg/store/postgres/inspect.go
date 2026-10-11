package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"opendustpan.org/opendustpan/pkg/store/internal/migrate"
	"opendustpan.org/opendustpan/pkg/store/schema"
)

// Inspect connects to the database at url read-only and reports its schema
// state: the recorded version, the version the binary needs, the pending
// migration names and the unknown recorded versions. It creates no table,
// applies no migration, takes no migration lock and opens no blob store. A
// database with no schema_migrations table reports version 0 with every
// migration pending.
func Inspect(ctx context.Context, databaseURL string) (schema.Inspection, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return schema.Inspection{}, fmt.Errorf("open postgres pool read-only: %w", err)
	}

	defer pool.Close()

	if pingErr := pool.Ping(ctx); pingErr != nil {
		return schema.Inspection{}, fmt.Errorf("connect to postgres read-only: %w", pingErr)
	}

	all, err := migrate.Load(migrations, "migrations")
	if err != nil {
		return schema.Inspection{}, err
	}

	//nolint:exhaustruct_v5 // A read-only inspection sets only the access mode.
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return schema.Inspection{}, fmt.Errorf("begin read-only transaction: %w", err)
	}

	defer func() {
		if rollbackErr := tx.Rollback(ctx); rollbackErr != nil {
			_ = rollbackErr
		}
	}()

	recorded, err := inspectRecorded(ctx, tx)
	if err != nil {
		return schema.Inspection{}, err
	}

	holds, err := inspectHoldsOpenTofuState(ctx, tx)
	if err != nil {
		return schema.Inspection{}, err
	}

	inspection := migrate.Inspect(recorded, all)
	inspection.HoldsOpenTofuState = holds

	return inspection, nil
}

// inspectHoldsOpenTofuState reports whether the state_versions table exists
// and holds at least one row. A database without that table holds no
// OpenTofu state.
func inspectHoldsOpenTofuState(ctx context.Context, tx pgx.Tx) (bool, error) {
	var exists bool

	err := tx.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM information_schema.tables
		WHERE table_schema = current_schema() AND table_name = 'state_versions'
	)`).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check state_versions: %w", err)
	}

	if !exists {
		return false, nil
	}

	var count int64

	if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM state_versions`).Scan(&count); err != nil {
		return false, fmt.Errorf("count state versions: %w", err)
	}

	return count > 0, nil
}

// inspectRecorded returns the recorded versions, ordered, in the read-only
// transaction. It returns nothing when the database has no
// schema_migrations table, and it takes no migration lock.
func inspectRecorded(ctx context.Context, tx pgx.Tx) ([]int64, error) {
	var exists bool

	err := tx.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM information_schema.tables
		WHERE table_schema = current_schema() AND table_name = 'schema_migrations'
	)`).Scan(&exists)
	if err != nil {
		return nil, fmt.Errorf("check schema_migrations: %w", err)
	}

	if !exists {
		return nil, nil
	}

	rows, err := tx.Query(ctx, `SELECT version FROM schema_migrations ORDER BY version`)
	if err != nil {
		return nil, fmt.Errorf("read schema versions: %w", err)
	}

	defer rows.Close()

	var out []int64

	for rows.Next() {
		var version int64
		if scanErr := rows.Scan(&version); scanErr != nil {
			rows.Close()

			return nil, fmt.Errorf("read schema versions: %w", scanErr)
		}

		out = append(out, version)
	}

	rows.Close()

	if iterErr := rows.Err(); iterErr != nil {
		return nil, fmt.Errorf("read schema versions: %w", iterErr)
	}

	return out, nil
}

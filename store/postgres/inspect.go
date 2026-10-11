package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"opendustpan.org/opendustpan/store/internal/migrate"
	"opendustpan.org/opendustpan/store/schema"
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

	recorded, err := inspectRecorded(ctx, pool)
	if err != nil {
		return schema.Inspection{}, err
	}

	return migrate.Inspect(recorded, all), nil
}

// inspectRecorded returns the recorded versions, ordered, in one read-only
// transaction. It returns nothing when the database has no
// schema_migrations table, and it takes no migration lock.
func inspectRecorded(ctx context.Context, pool *pgxpool.Pool) ([]int64, error) {
	//nolint:exhaustruct_v5 // A read-only inspection sets only the access mode.
	// Isolation, deferrability and begin text stay at the pgx defaults.
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, fmt.Errorf("begin read-only transaction: %w", err)
	}

	defer func() {
		if rollbackErr := tx.Rollback(ctx); rollbackErr != nil {
			_ = rollbackErr
		}
	}()

	var exists bool

	err = tx.QueryRow(ctx, `SELECT EXISTS (
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

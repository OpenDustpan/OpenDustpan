package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/store/schema"
)

// AppliedMigrations returns the rows of schema_migrations ordered by
// version, in one read-only transaction without a migration lock. A schema
// with no schema_migrations table reports nothing. The installation API
// answers them for `opendustpan upgrade check`.
func (s *Store) AppliedMigrations(ctx context.Context) ([]schema.AppliedMigration, error) {
	//nolint:exhaustruct_v5 // A read-only inspection sets only the access mode.
	// Isolation, deferrability and begin text stay at the pgx defaults.
	tx, err := s.txs.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
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

	query, err := appliedQuery(ctx, tx)
	if err != nil {
		return nil, err
	}

	rows, err := tx.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("read schema migrations: %w", err)
	}

	defer rows.Close()

	var out []schema.AppliedMigration

	for rows.Next() {
		var applied schema.AppliedMigration

		if scanErr := rows.Scan(&applied.Version, &applied.Name, &applied.MinBinaryVersion, &applied.SHA256); scanErr != nil {
			rows.Close()

			return nil, fmt.Errorf("read schema migrations: %w", scanErr)
		}

		out = append(out, applied)
	}

	rows.Close()

	if iterErr := rows.Err(); iterErr != nil {
		return nil, fmt.Errorf("read schema migrations: %w", iterErr)
	}

	return out, nil
}

// EmbeddedMigrations returns the names of the migrations the binary embeds,
// ordered by version, with the newest version. The names come from the
// shared schema list, so the upgrade check verdict never depends on which
// dialect the server runs. It still loads the schema file, so a bad header
// fails here and not only in the adapter tests that pin the list.
func EmbeddedMigrations() ([]string, int64, error) {
	if _, err := embeddedMigrations(); err != nil {
		return nil, 0, err
	}

	names, newest := schema.EmbeddedMigrationNames()

	return names, newest, nil
}

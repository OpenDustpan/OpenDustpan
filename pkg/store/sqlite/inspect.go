package sqlite

import (
	"context"
	"fmt"

	"opendustpan.org/opendustpan/pkg/store/internal/migrate"
	"opendustpan.org/opendustpan/pkg/store/schema"
)

// AppliedMigrations returns the rows of schema_migrations ordered by
// version. It opens nothing, migrates nothing and takes no lock. A database
// with no schema_migrations table reports nothing. The installation API
// answers them for `opendustpan upgrade check`.
func (s *Store) AppliedMigrations(ctx context.Context) ([]schema.AppliedMigration, error) {
	var exists int64

	err := s.conn.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'schema_migrations'`,
	).Scan(&exists)
	if err != nil {
		return nil, fmt.Errorf("check schema_migrations: %w", err)
	}

	if exists == 0 {
		return nil, nil
	}

	rows, err := s.conn.QueryContext(ctx, `SELECT version, name FROM schema_migrations ORDER BY version`)
	if err != nil {
		return nil, fmt.Errorf("read schema migrations: %w", err)
	}

	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			_ = closeErr
		}
	}()

	var out []schema.AppliedMigration

	for rows.Next() {
		var applied schema.AppliedMigration

		if scanErr := rows.Scan(&applied.Version, &applied.Name); scanErr != nil {
			return nil, fmt.Errorf("read schema migrations: %w", scanErr)
		}

		out = append(out, applied)
	}

	if iterErr := rows.Err(); iterErr != nil {
		return nil, fmt.Errorf("read schema migrations: %w", iterErr)
	}

	return out, nil
}

// EmbeddedMigrations returns the names of the migrations the binary embeds,
// ordered by version, with the newest version. The names come from the
// shared schema list, so the upgrade check verdict never depends on which
// dialect the server runs. It still loads the embedded directory, so a bad
// file name fails here and not only in the adapter tests that pin the list.
func EmbeddedMigrations() ([]string, int64, error) {
	if _, err := migrate.Load(migrations, "migrations"); err != nil {
		return nil, 0, err
	}

	names, newest := schema.EmbeddedMigrationNames()

	return names, newest, nil
}

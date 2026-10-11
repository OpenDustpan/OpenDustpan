package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"path/filepath"

	"opendustpan.org/opendustpan/pkg/store/internal/migrate"
	"opendustpan.org/opendustpan/pkg/store/schema"
)

// Inspect opens the database at path read-only and reports its schema state:
// the recorded version, the version the binary needs, the pending migration
// names and the unknown recorded versions. It creates no table, applies no
// migration and opens no blob store. A database with no schema_migrations
// table reports version 0 with every migration pending. A missing file is an
// error that names the path. It creates no file.
func Inspect(ctx context.Context, path string) (schema.Inspection, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return schema.Inspection{}, fmt.Errorf("resolve database path: %w", err)
	}

	query := url.Values{}
	query.Set("mode", "ro")
	query.Add("_pragma", "query_only(on)")
	query.Add("_pragma", "busy_timeout(10000)")
	query.Add("_pragma", "foreign_keys(on)")

	dsn := (&url.URL{Scheme: fileScheme, Path: abs, RawQuery: query.Encode()}).String()

	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return schema.Inspection{}, fmt.Errorf("open sqlite database read-only %s: %w", path, err)
	}

	defer func() {
		if closeErr := conn.Close(); closeErr != nil {
			_ = closeErr
		}
	}()

	if pingErr := conn.PingContext(ctx); pingErr != nil {
		return schema.Inspection{}, fmt.Errorf("open sqlite database read-only %s: %w", path, pingErr)
	}

	all, err := migrate.Load(migrations, "migrations")
	if err != nil {
		return schema.Inspection{}, err
	}

	recorded, err := inspectRecorded(ctx, conn)
	if err != nil {
		return schema.Inspection{}, err
	}

	return migrate.Inspect(recorded, all), nil
}

// inspectRecorded returns the recorded versions, ordered. It returns nothing
// when the database has no schema_migrations table.
func inspectRecorded(ctx context.Context, conn *sql.DB) ([]int64, error) {
	var exists int64

	err := conn.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'schema_migrations'`,
	).Scan(&exists)
	if err != nil {
		return nil, fmt.Errorf("check schema_migrations: %w", err)
	}

	if exists == 0 {
		return nil, nil
	}

	rows, err := conn.QueryContext(ctx, `SELECT version FROM schema_migrations ORDER BY version`)
	if err != nil {
		return nil, fmt.Errorf("read schema versions: %w", err)
	}

	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			_ = closeErr
		}
	}()

	var out []int64

	for rows.Next() {
		var version int64

		if scanErr := rows.Scan(&version); scanErr != nil {
			return nil, fmt.Errorf("read schema versions: %w", scanErr)
		}

		out = append(out, version)
	}

	if iterErr := rows.Err(); iterErr != nil {
		return nil, fmt.Errorf("read schema versions: %w", iterErr)
	}

	return out, nil
}

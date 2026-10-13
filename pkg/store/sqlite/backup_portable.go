package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"opendustpan.org/opendustpan/pkg/backup"
)

// ExportPortable implements backup.PortableExporter. It checks the column lists
// against the live schema (ADR 42, decision 5), copies the database into a
// snapshot with VACUUM INTO (decision 6), reads the summary from the snapshot,
// and writes one table file for each table of backup.Tables from the snapshot.
// The snapshot is removed before it returns and is never part of the backup.
func (s *Store) ExportPortable(
	ctx context.Context, sink backup.PortableSink,
) (backup.Summary, []backup.PortableFile, error) {
	if err := s.CheckCoverage(ctx); err != nil {
		return backup.Summary{}, nil, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	snap, err := s.capture(ctx)
	if err != nil {
		return backup.Summary{}, nil, err
	}

	defer removeSnapshot(snap)

	sum, err := summarizeFile(ctx, snap)
	if err != nil {
		return backup.Summary{}, nil, err
	}

	conn, err := openSnapshot(ctx, snap)
	if err != nil {
		return backup.Summary{}, nil, err
	}

	defer func() {
		if closeErr := conn.Close(); closeErr != nil {
			_ = closeErr
		}
	}()

	files, counts, err := backup.WritePortable(ctx, sink, sqliteReader(conn))
	if err != nil {
		return backup.Summary{}, nil, err
	}

	sum.RowCounts = counts

	return sum, files, nil
}

// sqliteReader reads the rows of one table from the snapshot. A SQLite time of 0
// is a missing time, so it is written as null (ADR 42, decision 3).
func sqliteReader(conn *sql.DB) backup.PortableReader {
	return func(ctx context.Context, table backup.PortableTable, emit func(values []any) error) error {
		rows, err := conn.QueryContext(ctx, sqliteSelect(table))
		if err != nil {
			return fmt.Errorf("select rows of table %q: %w", table.Table, backup.ErrExportRead)
		}

		defer func() {
			if closeErr := rows.Close(); closeErr != nil {
				_ = closeErr
			}
		}()

		for rows.Next() {
			values, scanErr := scanSQLiteRow(rows, table)
			if scanErr != nil {
				return scanErr
			}

			if emitErr := emit(values); emitErr != nil {
				return emitErr
			}
		}

		if rowsErr := rows.Err(); rowsErr != nil {
			return fmt.Errorf("read rows of table %q: %w", table.Table, backup.ErrExportRead)
		}

		return nil
	}
}

// scanSQLiteRow scans the current row of rows into one value for each column of
// table, with a missing time written as nil.
func scanSQLiteRow(rows *sql.Rows, table backup.PortableTable) ([]any, error) {
	values := make([]any, len(table.Columns))
	pointers := make([]any, len(table.Columns))

	for i := range values {
		pointers[i] = &values[i]
	}

	if err := rows.Scan(pointers...); err != nil {
		return nil, fmt.Errorf("scan row of table %q: %w", table.Table, backup.ErrExportRead)
	}

	for i, column := range table.Columns {
		if column.Kind == backup.KindTime && values[i] == int64(0) {
			values[i] = nil
		}
	}

	return values, nil
}

// sqliteSelect returns the query of the portable columns of table in their
// order, sorted by its ordering columns. The names are constants from the spec.
func sqliteSelect(table backup.PortableTable) string {
	columns := make([]string, 0, len(table.Columns))
	for _, column := range table.Columns {
		columns = append(columns, `"`+sqliteColumnName(table.Table, column.Name)+`"`)
	}

	orderBy := make([]string, 0, len(table.OrderBy))
	for _, name := range table.OrderBy {
		orderBy = append(orderBy, `"`+sqliteColumnName(table.Table, name)+`"`)
	}

	return "SELECT " + strings.Join(columns, ", ") + // #nosec G202 // Constant names from the portable spec.
		` FROM "` + table.Table + `" ORDER BY ` + strings.Join(orderBy, ", ")
}

// sqliteColumnName returns the SQLite column of a portable column name. The one
// column that differs is checkpoints.features, which SQLite names features_json
// (ADR 42, decision 3).
func sqliteColumnName(table, name string) string {
	if table == "checkpoints" && name == "features" {
		return "features_json"
	}

	return name
}

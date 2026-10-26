package postgres

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/backup"
)

// ExportPortable implements backup.PortableExporter. It checks the column lists
// against the live schema (ADR 42, decision 5), then reads every table of
// backup.Tables in the one repeatable-read, read-only transaction of the
// capture (decision 6), and writes one table file for each table. The summary
// is read in the same transaction, so every file and the summary describe one
// point in time.
func (s *Store) ExportPortable(
	ctx context.Context, sink backup.PortableSink,
) (backup.Summary, []backup.PortableFile, error) {
	if err := s.CheckCoverage(ctx); err != nil {
		return backup.Summary{}, nil, err
	}

	var (
		files []backup.PortableFile
		sum   backup.Summary
	)

	// The export reads every table in one snapshot, so it takes the swap lock
	// before the snapshot like every other snapshot read.
	err := inBackupTx(ctx, s.pool, s.snapshotOptions(), func(tx pgx.Tx) error {
		var counts map[string]int64

		var err error

		files, counts, err = backup.WritePortable(ctx, sink, pgReader(tx))
		if err != nil {
			return err
		}

		attrs, err := backupAttrs(ctx, s.queries.WithTx(tx), tx)
		if err != nil {
			return err
		}

		sum = backup.Summary{
			Dialect:       backup.DialectPostgres,
			HomeRegions:   attrs.regions,
			WrapperIDs:    attrs.wrappers,
			RowCounts:     counts,
			Blobs:         attrs.blobs,
			Unverified:    attrs.unverified,
			SchemaVersion: attrs.version,
		}

		return nil
	})
	if err != nil {
		return backup.Summary{}, nil, err
	}

	return sum, files, nil
}

// pgReader reads the rows of one table inside tx. A jsonb column is read as its
// JSON text, so the portable writer holds the value as JSON.
func pgReader(tx pgx.Tx) backup.PortableReader {
	return func(ctx context.Context, table backup.PortableTable, emit func(values []any) error) error {
		rows, err := tx.Query(ctx, pgSelect(table))
		if err != nil {
			return fmt.Errorf("select rows of table %q: %w", table.Table, backup.ErrExportRead)
		}

		defer rows.Close()

		for rows.Next() {
			values := make([]any, len(table.Columns))
			pointers := make([]any, len(table.Columns))

			for i := range values {
				pointers[i] = &values[i]
			}

			if scanErr := rows.Scan(pointers...); scanErr != nil {
				return fmt.Errorf("scan row of table %q: %w", table.Table, backup.ErrExportRead)
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

// pgSelect returns the query of the portable columns of table in their order,
// sorted by its ordering columns. A JSON column is cast to text. The names are
// constants from the spec.
func pgSelect(table backup.PortableTable) string {
	columns := make([]string, 0, len(table.Columns))
	for _, column := range table.Columns {
		ident := pgIdent(column.Name)
		if column.Kind == backup.KindJSON {
			ident = "CAST(" + ident + " AS text)"
		}

		columns = append(columns, ident)
	}

	orderBy := make([]string, 0, len(table.OrderBy))
	for _, name := range table.OrderBy {
		orderBy = append(orderBy, pgIdent(name))
	}

	return "SELECT " + strings.Join(columns, ", ") + // #nosec G202 // Constant names from the portable spec.
		" FROM " + pgIdent(table.Table) + " ORDER BY " + strings.Join(orderBy, ", ")
}

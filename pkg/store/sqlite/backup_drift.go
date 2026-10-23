package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/store/internal/driftstore"
)

// sqliteDriftBackupTables holds the backup columns of the drift result table,
// in the order of backup.Tables.
func sqliteDriftBackupTables() []sqliteBackupTable {
	return []sqliteBackupTable{
		{
			table: "drift_results",
			columns: []string{
				backupColUnitID,
				backupColNamespaceID,
				backupColTool,
				backupColJobID,
				backupColStatus,
				"checked_at_ms",
				"truncated",
				"resources_json",
			},
		},
	}
}

// checkStagedDriftResults checks each drift result row of a staged backup with
// the rules of a new result. A row that fails makes the backup corrupt, so a
// restore never brings in a result that the server would not have stored. It
// returns backup.ErrCorrupt.
//
// Defense in depth: the store checks a row again when it reads it. This check
// stays so that a bad row fails the restore and does not wait for a reader.
func checkStagedDriftResults(ctx context.Context, conn *sql.DB) error {
	const query = `SELECT d.unit_id, n.name, d.tool, d.job_id, d.status, d.checked_at_ms, d.truncated, d.resources_json
FROM drift_results AS d LEFT JOIN namespaces AS n ON n.id = d.namespace_id`

	rows, err := conn.QueryContext(ctx, query)
	if err != nil {
		return fmt.Errorf("read staged drift results: %w", backup.ErrExportRead)
	}

	defer func() {
		_ = rows.Close() //nolint:errcheck // Read-only check handle.
	}()

	for rows.Next() {
		var (
			row       driftstore.Row
			namespace sql.NullString
			truncated int64
		)

		scanErr := rows.Scan(&row.UnitID, &namespace, &row.Tool, &row.JobID, &row.Status, &row.CheckedAtMs,
			&truncated, &row.ResourcesJSON)
		if scanErr != nil {
			return fmt.Errorf("read staged drift results: %w", backup.ErrExportRead)
		}

		// A result of a namespace that the backup does not hold is corrupt. The
		// LEFT JOIN keeps such a row in the result, and the NULL name fails it.
		if !namespace.Valid {
			return fmt.Errorf("staged drift result of unit %q has no namespace: %w", row.UnitID, backup.ErrCorrupt)
		}

		row.Namespace, row.Truncated = namespace.String, truncated == 1

		if _, rowErr := driftstore.Latest(&row); rowErr != nil {
			return fmt.Errorf("staged drift result of unit %q is not valid: %w: %w", row.UnitID, rowErr,
				backup.ErrCorrupt)
		}
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		return fmt.Errorf("read staged drift results: %w", backup.ErrExportRead)
	}

	return nil
}

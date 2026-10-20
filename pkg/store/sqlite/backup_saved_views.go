package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/store/internal/viewstore"
)

// backupColOwner is the column that holds the owner of a saved view.
const backupColOwner = "owner"

// sqliteSavedViewBackupTables holds the backup columns of the saved view
// table, in the order of backup.Tables.
func sqliteSavedViewBackupTables() []sqliteBackupTable {
	return []sqliteBackupTable{
		{
			table: "saved_views",
			columns: []string{
				"id",
				backupColNamespaceID,
				backupColOwner,
				backupColName,
				"query_json",
				backupColCreatedAtMs,
			},
		},
	}
}

// checkStagedSavedViews checks each saved view row of a staged backup with the
// rules of a new view. A row that fails makes the backup corrupt, so a restore
// never brings in a view that the server would not have stored. It returns
// backup.ErrCorrupt.
//
// Defense in depth: the store checks a row again when it reads it. This check
// stays so that a bad row fails the restore and does not wait for a reader.
func checkStagedSavedViews(ctx context.Context, conn *sql.DB) error {
	const query = `SELECT v.id, n.name, v.owner, v.name, v.query_json, v.created_at_ms
FROM saved_views AS v LEFT JOIN namespaces AS n ON n.id = v.namespace_id`

	rows, err := conn.QueryContext(ctx, query)
	if err != nil {
		return fmt.Errorf("read staged saved views: %w", backup.ErrExportRead)
	}

	defer func() {
		_ = rows.Close() //nolint:errcheck // Read-only check handle.
	}()

	for rows.Next() {
		var (
			row       viewstore.Row
			namespace sql.NullString
		)

		scanErr := rows.Scan(&row.ID, &namespace, &row.Owner, &row.Name, &row.QueryJSON, &row.CreatedAtMs)
		if scanErr != nil {
			return fmt.Errorf("read staged saved views: %w", backup.ErrExportRead)
		}

		// A view of a namespace that the backup does not hold is corrupt. The
		// LEFT JOIN keeps such a row in the result, and the NULL name fails it.
		if !namespace.Valid {
			return fmt.Errorf("staged saved view %q has no namespace: %w", row.ID, backup.ErrCorrupt)
		}

		row.Namespace = namespace.String

		if _, viewErr := viewstore.View(row); viewErr != nil {
			return fmt.Errorf("staged saved view %q is not valid: %w: %w", row.ID, viewErr, backup.ErrCorrupt)
		}
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		return fmt.Errorf("read staged saved views: %w", backup.ErrExportRead)
	}

	return nil
}

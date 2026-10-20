package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/store/internal/viewstore"
)

// pgCopySavedViewTables holds the COPY columns of the saved view table, in the
// order of backup.Tables.
func pgCopySavedViewTables() []pgCopyTable {
	return []pgCopyTable{
		{
			table:   "saved_views",
			columns: []string{"id", pgColNamespaceID, pgColOwner, pgColName, "query_json", pgColCreatedAtMs},
			orderBy: []string{"id"},
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
func checkStagedSavedViews(ctx context.Context, pool *pgxpool.Pool, staging string) error {
	query := "SELECT v.id, n.name, v.owner, v.name, v.query_json, v.created_at_ms FROM " +
		stagingTable(staging, "saved_views") + " AS v JOIN " + stagingTable(staging, "namespaces") +
		" AS n ON n.id = v.namespace_id"

	rows, err := pool.Query(ctx, query)
	if err != nil {
		return fmt.Errorf("read staged saved views: %w", backup.ErrExportRead)
	}

	defer rows.Close()

	for rows.Next() {
		var row viewstore.Row

		scanErr := rows.Scan(&row.ID, &row.Namespace, &row.Owner, &row.Name, &row.QueryJSON, &row.CreatedAtMs)
		if scanErr != nil {
			return fmt.Errorf("read staged saved views: %w", backup.ErrExportRead)
		}

		if _, viewErr := viewstore.View(row); viewErr != nil {
			return fmt.Errorf("staged saved view %q is not valid: %w: %w", row.ID, viewErr, backup.ErrCorrupt)
		}
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		return fmt.Errorf("read staged saved views: %w", backup.ErrExportRead)
	}

	return nil
}

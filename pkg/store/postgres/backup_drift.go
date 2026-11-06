package postgres

import (
	"context"
	"fmt"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/store/internal/driftstore"
)

// pgCopyDriftTables holds the COPY columns of the drift result table, in the
// order of backup.Tables.
func pgCopyDriftTables() []pgCopyTable {
	return []pgCopyTable{
		{
			table: "drift_results",
			columns: []string{
				pgColUnitID, pgColNamespaceID, pgColTool, pgColJobID, pgColStatus, "checked_at_ms", "truncated",
				"resources_json",
			},
			orderBy: []string{pgColUnitID},
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
func checkStagedDriftResults(ctx context.Context, pool pgQuerier, staging string) error {
	query := "SELECT d.unit_id, n.name, d.tool, d.job_id, d.status, d.checked_at_ms, d.truncated, d.resources_json FROM " +
		stagingTable(
			staging,
			"drift_results",
		) + " AS d LEFT JOIN " + stagingTable(
		staging,
		"namespaces",
	) +
		" AS n ON n.id = d.namespace_id"

	rows, err := pool.Query(ctx, query)
	if err != nil {
		return fmt.Errorf("read staged drift results: %w", backup.ErrExportRead)
	}

	defer rows.Close()

	for rows.Next() {
		var (
			row       driftstore.Row
			namespace *string
		)

		scanErr := rows.Scan(&row.UnitID, &namespace, &row.Tool, &row.JobID, &row.Status, &row.CheckedAtMs,
			&row.Truncated, &row.ResourcesJSON)
		if scanErr != nil {
			return fmt.Errorf("read staged drift results: %w", backup.ErrExportRead)
		}

		// A result of a namespace that the backup does not hold is corrupt. The
		// LEFT JOIN keeps such a row in the result, and the NULL name fails it.
		if namespace == nil {
			return fmt.Errorf("staged drift result of unit %q has no namespace: %w", row.UnitID, backup.ErrCorrupt)
		}

		row.Namespace = *namespace

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

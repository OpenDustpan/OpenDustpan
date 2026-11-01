package postgres

import (
	"context"
	"fmt"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/unitsettings"
)

// pgCopyUnitSettingsTables holds the COPY columns of the unit settings tables,
// in the order of backup.Tables.
func pgCopyUnitSettingsTables() []pgCopyTable {
	return []pgCopyTable{
		{
			table:   "unit_settings",
			columns: []string{pgColUnitID, pgColNamespaceID, pgColVersion, pgColBody, pgColUpdatedAtMs},
			orderBy: []string{pgColUnitID},
		},
		{
			table:   "unit_defaults",
			columns: []string{pgColNamespaceID, "pool", pgColVersion, pgColUpdatedAtMs},
			orderBy: []string{pgColNamespaceID},
		},
	}
}

// checkStagedUnitSettings checks each unit settings row and each unit defaults
// row of a staged backup with the rules of a new write. A row that fails makes
// the backup corrupt, so a restore never brings in settings that the server
// would not have stored. It returns backup.ErrCorrupt.
//
// Defense in depth: the store checks a row again when it reads it, and the
// portable reader checks each value. This check stays so that a bad row fails
// the restore and does not wait for a reader.
func checkStagedUnitSettings(ctx context.Context, pool pgQuerier, staging string) error {
	query := "SELECT s.unit_id, n.name, s.version, s.body FROM " + stagingTable(staging, "unit_settings") +
		" AS s LEFT JOIN " + stagingTable(staging, "namespaces") + " AS n ON n.id = s.namespace_id"

	rows, err := pool.Query(ctx, query)
	if err != nil {
		return fmt.Errorf("read staged unit settings: %w", backup.ErrExportRead)
	}

	defer rows.Close()

	for rows.Next() {
		var (
			unitID, body string
			namespace    *string
			version      int64
		)

		if scanErr := rows.Scan(&unitID, &namespace, &version, &body); scanErr != nil {
			return fmt.Errorf("read staged unit settings: %w", backup.ErrExportRead)
		}

		// A record of a namespace that the backup does not hold is corrupt. The
		// LEFT JOIN keeps such a row in the result, and the NULL name fails it.
		if namespace == nil || version < 1 {
			return fmt.Errorf("staged unit settings of unit %q have no namespace or a bad version: %w", unitID,
				backup.ErrCorrupt)
		}

		if _, parseErr := unitsettings.Parse([]byte(body)); parseErr != nil {
			return fmt.Errorf("staged unit settings of unit %q are not valid: %w: %w", unitID, parseErr,
				backup.ErrCorrupt)
		}
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		return fmt.Errorf("read staged unit settings: %w", backup.ErrExportRead)
	}

	return checkStagedUnitDefaults(ctx, pool, staging)
}

// checkStagedUnitDefaults checks each unit defaults row of a staged backup.
func checkStagedUnitDefaults(ctx context.Context, pool pgQuerier, staging string) error {
	query := "SELECT n.name, d.pool, d.version FROM " + stagingTable(staging, "unit_defaults") +
		" AS d LEFT JOIN " + stagingTable(staging, "namespaces") + " AS n ON n.id = d.namespace_id"

	rows, err := pool.Query(ctx, query)
	if err != nil {
		return fmt.Errorf("read staged unit defaults: %w", backup.ErrExportRead)
	}

	defer rows.Close()

	for rows.Next() {
		var (
			namespace *string
			poolID    string
			version   int64
		)

		if scanErr := rows.Scan(&namespace, &poolID, &version); scanErr != nil {
			return fmt.Errorf("read staged unit defaults: %w", backup.ErrExportRead)
		}

		if namespace == nil || version < 1 {
			return fmt.Errorf("staged unit defaults have no namespace or a bad version: %w", backup.ErrCorrupt)
		}

		if poolErr := unitsettings.CheckPool(poolID); poolErr != nil {
			return fmt.Errorf("staged unit defaults of %q are not valid: %w: %w", *namespace, poolErr,
				backup.ErrCorrupt)
		}
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		return fmt.Errorf("read staged unit defaults: %w", backup.ErrExportRead)
	}

	return nil
}

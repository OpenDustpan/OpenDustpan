package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/unitsettings"
)

// sqliteUnitSettingsBackupTables holds the backup columns of the unit settings
// tables, in the order of backup.Tables.
func sqliteUnitSettingsBackupTables() []sqliteBackupTable {
	return []sqliteBackupTable{
		{
			table: "unit_settings",
			columns: []string{
				backupColUnitID, backupColNamespaceID, backupColVersion, backupColBody, backupColUpdatedAtMs,
			},
		},
		{
			table:   "unit_defaults",
			columns: []string{backupColNamespaceID, "pool", backupColVersion, backupColUpdatedAtMs},
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
func checkStagedUnitSettings(ctx context.Context, conn *sql.DB) (retErr error) {
	const settings = `SELECT s.unit_id, n.name, s.version, s.body
FROM unit_settings AS s LEFT JOIN namespaces AS n ON n.id = s.namespace_id`

	rows, err := conn.QueryContext(ctx, settings)
	if err != nil {
		return fmt.Errorf("read staged unit settings: %w", backup.ErrExportRead)
	}

	defer func() {
		if closeErr := rows.Close(); closeErr != nil && retErr == nil {
			retErr = fmt.Errorf("close staged unit settings: %w", backup.ErrExportRead)
		}
	}()

	for rows.Next() {
		var (
			unitID, body string
			namespace    sql.NullString
			version      int64
		)

		if scanErr := rows.Scan(&unitID, &namespace, &version, &body); scanErr != nil {
			return fmt.Errorf("read staged unit settings: %w", backup.ErrExportRead)
		}

		// A record of a namespace that the backup does not hold is corrupt. The
		// LEFT JOIN keeps such a row in the result, and the NULL name fails it.
		if !namespace.Valid || version < 1 {
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

	return checkStagedUnitDefaults(ctx, conn)
}

// checkStagedUnitDefaults checks each unit defaults row of a staged backup.
func checkStagedUnitDefaults(ctx context.Context, conn *sql.DB) (retErr error) {
	const defaults = `SELECT n.name, d.pool, d.version
FROM unit_defaults AS d LEFT JOIN namespaces AS n ON n.id = d.namespace_id`

	rows, err := conn.QueryContext(ctx, defaults)
	if err != nil {
		return fmt.Errorf("read staged unit defaults: %w", backup.ErrExportRead)
	}

	defer func() {
		if closeErr := rows.Close(); closeErr != nil && retErr == nil {
			retErr = fmt.Errorf("close staged unit settings: %w", backup.ErrExportRead)
		}
	}()

	for rows.Next() {
		var (
			namespace sql.NullString
			pool      string
			version   int64
		)

		if scanErr := rows.Scan(&namespace, &pool, &version); scanErr != nil {
			return fmt.Errorf("read staged unit defaults: %w", backup.ErrExportRead)
		}

		if !namespace.Valid || version < 1 {
			return fmt.Errorf("staged unit defaults have no namespace or a bad version: %w", backup.ErrCorrupt)
		}

		if poolErr := unitsettings.CheckPool(pool); poolErr != nil {
			return fmt.Errorf("staged unit defaults of %q are not valid: %w: %w", namespace.String, poolErr,
				backup.ErrCorrupt)
		}
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		return fmt.Errorf("read staged unit defaults: %w", backup.ErrExportRead)
	}

	return nil
}

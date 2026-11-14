package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/store/internal/triggerstore"
	"opendustpan.org/opendustpan/pkg/store/sqlite/internal/db"
)

// sqliteTriggerBackupTables holds the backup columns of the trigger table, in
// the order of backup.Tables.
func sqliteTriggerBackupTables() []sqliteBackupTable {
	return []sqliteBackupTable{
		{
			table: "triggers",
			columns: []string{
				backupColNamespaceID,
				"id",
				"unit_tool",
				backupColUnitID,
				backupColKind,
				"preset",
				"cron",
				"source_namespace",
				"source_tool",
				"source_unit_id",
				"enabled",
				"last_saver_subject",
				"last_saver_id",
				backupColCreatedAtMs,
				backupColUpdatedAtMs,
				"next_slot_ms",
			},
		},
	}
}

// checkStagedTriggers checks each trigger row of a staged backup with the rules
// of a stored trigger and of its next slot, against the clock of the store. A
// row that fails makes the backup corrupt, so a restore never brings in a
// trigger that the server would not serve. It returns backup.ErrCorrupt.
//
// Defense in depth: the store checks a row again when it reads it. This check
// stays so that a bad row fails the restore and does not wait for a reader.
func checkStagedTriggers(ctx context.Context, conn *sql.DB) error {
	nowMs, err := storeNow(ctx, db.New(conn))
	if err != nil {
		return err
	}

	now := time.UnixMilli(nowMs).UTC()

	const query = `SELECT n.name, t.id, t.unit_tool, t.unit_id, t.kind, t.preset, t.cron, t.source_namespace,
t.source_tool, t.source_unit_id, t.enabled, t.last_saver_subject, t.last_saver_id, t.created_at_ms, t.updated_at_ms,
t.next_slot_ms
FROM triggers AS t LEFT JOIN namespaces AS n ON n.id = t.namespace_id`

	rows, err := conn.QueryContext(ctx, query)
	if err != nil {
		return fmt.Errorf("read staged triggers: %w", backup.ErrExportRead)
	}

	defer func() {
		_ = rows.Close() //nolint:errcheck // Read-only check handle.
	}()

	for rows.Next() {
		row, scanErr := scanStagedTrigger(rows)
		if scanErr != nil {
			return scanErr
		}

		if trErr := triggerstore.CheckStaged(row, now); trErr != nil {
			return fmt.Errorf("staged trigger %q is not valid: %w: %w", row.ID, trErr, backup.ErrCorrupt)
		}
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		return fmt.Errorf("read staged triggers: %w", backup.ErrExportRead)
	}

	return nil
}

// scanStagedTrigger reads the current row of rows. The first column is the name
// of the namespace, which is NULL when the backup does not hold the namespace of
// the trigger.
func scanStagedTrigger(rows *sql.Rows) (triggerstore.Row, error) {
	// An error returns the zero row, so that no caller reads a partial
	// trigger from it.
	var (
		row       triggerstore.Row
		none      triggerstore.Row
		namespace sql.NullString
	)

	scanErr := rows.Scan(
		&namespace, &row.ID, &row.UnitTool, &row.UnitID, &row.Kind, &row.Preset, &row.Cron,
		&row.SourceNamespace, &row.SourceTool, &row.SourceUnitID, &row.Enabled, &row.LastSaverSubject,
		&row.LastSaverID, &row.CreatedAtMs, &row.UpdatedAtMs, &row.NextSlotMs,
	)
	if scanErr != nil {
		return none, fmt.Errorf("read staged triggers: %w", backup.ErrExportRead)
	}

	// A trigger of a namespace that the backup does not hold is corrupt. The
	// LEFT JOIN keeps such a row in the result, and the NULL name fails it.
	if !namespace.Valid {
		return none, fmt.Errorf("staged trigger %q has no namespace: %w", row.ID, backup.ErrCorrupt)
	}

	row.Namespace = namespace.String

	return row, nil
}

package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/store/internal/triggerstore"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
)

// pgCopyTriggerTables holds the COPY columns of the trigger table, in the order
// of backup.Tables.
func pgCopyTriggerTables() []pgCopyTable {
	return []pgCopyTable{
		{
			table: "triggers",
			columns: []string{
				pgColNamespaceID, "id", "unit_tool", pgColUnitID, pgColKind, "preset", "cron", "source_namespace",
				"source_tool", "source_unit_id", "enabled", "last_saver_subject",
				"last_saver_id", pgColCreatedAtMs, pgColUpdatedAtMs, "next_slot_ms",
			},
			orderBy: []string{pgColNamespaceID, "id"},
		},
	}
}

// checkStagedTriggers checks each trigger row of a staged backup with the rules
// of a stored trigger and of its next slot, against the clock of the database
// in the same transaction. A row that fails makes the backup corrupt, so a
// restore never brings in a trigger that the server would not serve. It returns
// backup.ErrCorrupt.
//
// Defense in depth: the store checks a row again when it reads it. This check
// stays so that a bad row fails the restore and does not wait for a reader.
func checkStagedTriggers(ctx context.Context, pool db.DBTX, staging string) error {
	nowMs, err := storeNow(ctx, db.New(pool))
	if err != nil {
		return err
	}

	now := time.UnixMilli(nowMs).UTC()

	query := "SELECT n.name, t.id, t.unit_tool, t.unit_id, t.kind, t.preset, t.cron, t.source_namespace, " +
		"t.source_tool, t.source_unit_id, t.enabled, t.last_saver_subject, t.last_saver_id, t.created_at_ms, " +
		"t.updated_at_ms, t.next_slot_ms FROM " +
		stagingTable(staging, "triggers") + " AS t LEFT JOIN " + stagingTable(staging, "namespaces") +
		" AS n ON n.id = t.namespace_id"

	rows, err := pool.Query(ctx, query)
	if err != nil {
		return fmt.Errorf("read staged triggers: %w", backup.ErrExportRead)
	}

	defer rows.Close()

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
func scanStagedTrigger(rows pgx.Rows) (triggerstore.Row, error) {
	// An error returns the zero row, so that no caller reads a partial
	// trigger from it.
	var (
		row       triggerstore.Row
		none      triggerstore.Row
		namespace *string
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
	if namespace == nil {
		return none, fmt.Errorf("staged trigger %q has no namespace: %w", row.ID, backup.ErrCorrupt)
	}

	row.Namespace = *namespace

	return row, nil
}

package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/store/internal/sinkstore"
)

// Column names of the sink tables that other tables share.
const (
	sinkColScope      = "scope"
	sinkColSinkID     = "sink_id"
	sinkColPrincipal  = "principal"
	sinkColSubject    = "subject"
	sinkColRef        = "ref"
	sinkColCiphertext = "ciphertext"
)

// sqliteSinkBackupTables holds the backup columns of the notification sink
// tables, in the order of backup.Tables.
func sqliteSinkBackupTables() []sqliteBackupTable {
	return []sqliteBackupTable{
		{
			table: "notification_sinks",
			columns: []string{
				"id", sinkColScope, backupColKind, backupColName, "platform", "target", backupColCreatedBy,
				backupColVersion,
			},
		},
		{
			table:   "notification_sink_secrets",
			columns: []string{sinkColScope, sinkColRef, sinkColSinkID, "position", sinkColCiphertext},
		},
		{
			table:   "notification_sink_refs",
			columns: []string{sinkColScope, sinkColRef},
		},
		{
			table: "notifications",
			columns: []string{
				sinkColScope,
				backupColSequence,
				"id",
				"type",
				"object",
				"object_id",
				sinkColSubject,
				"agent",
				"severity",
				"summary",
				"links",
				"attributes",
				"recipients",
				"dedupe_key",
				backupColAtMs,
			},
		},
		{
			table:   "notification_sequences",
			columns: []string{sinkColScope, "last_sequence"},
		},
		{
			table:   "notification_cursors",
			columns: []string{backupColName, sinkColScope, backupColSequence},
		},
		{
			table: "notification_subscriptions",
			columns: []string{
				"id", sinkColScope, sinkColSinkID, "types", "objects", "min_severity", backupColState, "reason",
				backupColCreatedBy, backupColVersion,
			},
		},
		{
			table: "notification_deliveries",
			columns: []string{
				"id",
				sinkColScope,
				"notification_id",
				"subscription_id",
				sinkColSinkID,
				"contact",
				sinkColPrincipal,
				backupColState,
				"attempts",
				"next_at_ms",
				"last_error",
				"delivered_at_ms",
				backupColOwner,
				"lease_until_ms",
				backupColVersion,
			},
		},
	}
}

// checkStagedSinks checks the sink tables of a staged backup with the rules of
// a stored sink. A row that fails makes the backup corrupt. It returns
// backup.ErrCorrupt.
func checkStagedSinks(ctx context.Context, conn *sql.DB) error {
	sinks, err := stagedSinkRows(ctx, conn)
	if err != nil {
		return err
	}

	secrets, err := stagedSinkSecrets(ctx, conn)
	if err != nil {
		return err
	}

	held, err := stagedSinkRefs(ctx, conn)
	if err != nil {
		return err
	}

	if checkErr := sinkstore.CheckStaged(sinks, secrets, held); checkErr != nil {
		return fmt.Errorf("staged notification sinks: %w: %w", checkErr, backup.ErrCorrupt)
	}

	return nil
}

func stagedSinkRows(ctx context.Context, conn *sql.DB) ([]sinkstore.Row, error) {
	const query = `SELECT id, scope, kind, name, platform, target, created_by, version
FROM notification_sinks ORDER BY id`

	rows, err := conn.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("read staged sinks: %w", backup.ErrExportRead)
	}

	defer func() {
		_ = rows.Close() //nolint:errcheck // Read-only check handle.
	}()

	var out []sinkstore.Row

	for rows.Next() {
		var row sinkstore.Row

		scanErr := rows.Scan(&row.ID, &row.Namespace, &row.Kind, &row.Name, &row.Platform, &row.Target,
			&row.CreatedBy, &row.Version)
		if scanErr != nil {
			return nil, fmt.Errorf("read staged sinks: %w", backup.ErrExportRead)
		}

		// A sink of a scope that the backup does not hold is corrupt:
		// checkStagedNotificationScopes refuses it.
		out = append(out, row)
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, fmt.Errorf("read staged sinks: %w", backup.ErrExportRead)
	}

	return out, nil
}

func stagedSinkSecrets(ctx context.Context, conn *sql.DB) ([]sinkstore.Secret, error) {
	const query = `SELECT scope, ref, sink_id FROM notification_sink_secrets ORDER BY sink_id, position`

	rows, err := conn.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("read staged sink secrets: %w", backup.ErrExportRead)
	}

	defer func() {
		_ = rows.Close() //nolint:errcheck // Read-only check handle.
	}()

	var out []sinkstore.Secret

	for rows.Next() {
		var sec sinkstore.Secret

		if scanErr := rows.Scan(&sec.Namespace, &sec.Ref, &sec.SinkID); scanErr != nil {
			return nil, fmt.Errorf("read staged sink secrets: %w", backup.ErrExportRead)
		}

		out = append(out, sec)
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, fmt.Errorf("read staged sink secrets: %w", backup.ErrExportRead)
	}

	return out, nil
}

func stagedSinkRefs(ctx context.Context, conn *sql.DB) (map[string]bool, error) {
	const query = `SELECT scope, ref FROM notification_sink_refs`

	rows, err := conn.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("read staged sink refs: %w", backup.ErrExportRead)
	}

	defer func() {
		_ = rows.Close() //nolint:errcheck // Read-only check handle.
	}()

	held := map[string]bool{}

	for rows.Next() {
		var scope, ref string

		if scanErr := rows.Scan(&scope, &ref); scanErr != nil {
			return nil, fmt.Errorf("read staged sink refs: %w", backup.ErrExportRead)
		}

		held[scope+"/"+ref] = true
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, fmt.Errorf("read staged sink refs: %w", backup.ErrExportRead)
	}

	return held, nil
}

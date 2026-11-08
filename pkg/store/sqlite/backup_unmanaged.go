package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/internal/unmanagedstore"
)

// sqliteUnmanagedBackupTables holds the backup columns of the sweep target and
// sweep tables, in the order of backup.Tables.
func sqliteUnmanagedBackupTables() []sqliteBackupTable {
	return []sqliteBackupTable{
		{
			table: "unmanaged_targets",
			columns: []string{
				backupColNamespaceID,
				backupColName,
				backupColPoolID,
				"scopes_json",
				"command_json",
				"every_ms",
				"timeout_ms",
				backupColEnabled,
			},
		},
		{
			table: "unmanaged_scans",
			columns: []string{
				"id",
				backupColNamespaceID,
				backupColSeq,
				backupColTarget,
				backupColKind,
				backupColStatus,
				"started_by",
				backupColReason,
				"scopes_json",
				"counts_json",
				backupColCreatedAtMs,
				backupColEndedAtMs,
			},
		},
	}
}

// checkStagedUnmanaged checks each sweep target and each sweep of a staged
// backup with the rules of a stored row. A row that fails makes the backup
// corrupt, so a restore never brings in a target or a sweep that the server
// would not serve. It returns backup.ErrCorrupt.
//
// Defense in depth: the store checks a row again when it reads it. This check
// stays so that a bad row fails the restore and does not wait for a reader.
func checkStagedUnmanaged(ctx context.Context, conn *sql.DB) error {
	if err := checkStagedUnmanagedTargets(ctx, conn); err != nil {
		return err
	}

	return checkStagedUnmanagedScans(ctx, conn)
}

func checkStagedUnmanagedTargets(ctx context.Context, conn *sql.DB) error {
	const query = `SELECT n.name, t.name, p.name, t.scopes_json, t.command_json, t.every_ms, t.timeout_ms, t.enabled
FROM unmanaged_targets AS t
LEFT JOIN namespaces AS n ON n.id = t.namespace_id
LEFT JOIN runner_pools AS p ON p.id = t.pool_id`

	rows, err := conn.QueryContext(ctx, query)
	if err != nil {
		return fmt.Errorf("read staged sweep targets: %w", backup.ErrExportRead)
	}

	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			_ = closeErr
		}
	}()

	for rows.Next() {
		var (
			row  unmanagedstore.TargetRow
			ns   sql.NullString
			pool sql.NullString
		)

		scanErr := rows.Scan(
			&ns, &row.Name, &pool, &row.ScopesJSON, &row.CommandJSON, &row.EveryMs, &row.TimeoutMs, &row.Enabled,
		)
		if scanErr != nil {
			return fmt.Errorf("read staged sweep targets: %w", backup.ErrExportRead)
		}

		// A target of a namespace or of a pool that the backup does not hold is
		// corrupt. The LEFT JOINs keep such a row in the result, and the NULL
		// name fails it.
		if !ns.Valid {
			return fmt.Errorf("staged sweep target %q has no namespace: %w", row.Name, backup.ErrCorrupt)
		}

		if !pool.Valid {
			return fmt.Errorf("staged sweep target %q has no runner pool: %w", row.Name, backup.ErrCorrupt)
		}

		row.Namespace, row.Pool = ns.String, pool.String

		if _, targetErr := unmanagedstore.Target(namespace.Name(ns.String), row); targetErr != nil {
			return fmt.Errorf("staged sweep target %q is not valid: %w: %w", row.Name, targetErr, backup.ErrCorrupt)
		}
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		return fmt.Errorf("read staged sweep targets: %w", backup.ErrExportRead)
	}

	return nil
}

func checkStagedUnmanagedScans(ctx context.Context, conn *sql.DB) error {
	const query = `SELECT n.name, s.id, s.seq, s.target, s.kind, s.status, s.started_by, s.reason, s.scopes_json,
s.counts_json, s.created_at_ms, s.ended_at_ms
FROM unmanaged_scans AS s LEFT JOIN namespaces AS n ON n.id = s.namespace_id`

	rows, err := conn.QueryContext(ctx, query)
	if err != nil {
		return fmt.Errorf("read staged sweeps: %w", backup.ErrExportRead)
	}

	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			_ = closeErr
		}
	}()

	for rows.Next() {
		var (
			row unmanagedstore.ScanRow
			ns  sql.NullString
		)

		scanErr := rows.Scan(
			&ns, &row.ID, &row.Seq, &row.Target, &row.Kind, &row.Status, &row.StartedBy, &row.Reason,
			&row.ScopesJSON, &row.CountsJSON, &row.CreatedAtMs, &row.EndedAtMs,
		)
		if scanErr != nil {
			return fmt.Errorf("read staged sweeps: %w", backup.ErrExportRead)
		}

		// A sweep of a namespace that the backup does not hold is corrupt. The
		// LEFT JOIN keeps such a row in the result, and the NULL name fails it.
		if !ns.Valid {
			return fmt.Errorf("staged sweep %q has no namespace: %w", row.ID, backup.ErrCorrupt)
		}

		row.Namespace = ns.String

		if _, scanRowErr := unmanagedstore.Scan(namespace.Name(ns.String), row); scanRowErr != nil {
			return fmt.Errorf("staged sweep %q is not valid: %w: %w", row.ID, scanRowErr, backup.ErrCorrupt)
		}
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		return fmt.Errorf("read staged sweeps: %w", backup.ErrExportRead)
	}

	return nil
}

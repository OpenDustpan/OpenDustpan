package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/store/internal/sinkstore"
)

// sqliteSinkBackupTables holds the backup columns of the notification sink
// tables, in the order of backup.Tables.
func sqliteSinkBackupTables() []sqliteBackupTable {
	return []sqliteBackupTable{
		{
			table: "notification_sinks",
			columns: []string{
				"id", backupColNamespaceID, backupColKind, backupColName, "platform", "target", backupColCreatedBy,
				backupColVersion,
			},
		},
		{
			table:   "notification_sink_secrets",
			columns: []string{backupColNamespaceID, "ref", "sink_id", "position", "ciphertext"},
		},
		{
			table:   "notification_sink_refs",
			columns: []string{backupColNamespaceID, "ref"},
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
	const query = `SELECT k.id, n.name, k.kind, k.name, k.platform, k.target, k.created_by, k.version
FROM notification_sinks AS k LEFT JOIN namespaces AS n ON n.id = k.namespace_id ORDER BY k.id`

	rows, err := conn.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("read staged sinks: %w", backup.ErrExportRead)
	}

	defer func() {
		_ = rows.Close() //nolint:errcheck // Read-only check handle.
	}()

	var out []sinkstore.Row

	for rows.Next() {
		var (
			row       sinkstore.Row
			namespace sql.NullString
		)

		if scanErr := rows.Scan(&row.ID, &namespace, &row.Kind, &row.Name, &row.Platform, &row.Target, &row.CreatedBy,
			&row.Version); scanErr != nil {
			return nil, fmt.Errorf("read staged sinks: %w", backup.ErrExportRead)
		}

		// A sink of a namespace that the backup does not hold is corrupt. The
		// LEFT JOIN keeps such a row, and the NULL name fails it.
		if !namespace.Valid {
			return nil, fmt.Errorf("staged sink %q has no namespace: %w", row.ID, backup.ErrCorrupt)
		}

		row.Namespace = namespace.String
		out = append(out, row)
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, fmt.Errorf("read staged sinks: %w", backup.ErrExportRead)
	}

	return out, nil
}

func stagedSinkSecrets(ctx context.Context, conn *sql.DB) ([]sinkstore.Secret, error) {
	const query = `SELECT n.name, s.ref, s.sink_id
FROM notification_sink_secrets AS s LEFT JOIN namespaces AS n ON n.id = s.namespace_id
ORDER BY s.sink_id, s.position`

	rows, err := conn.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("read staged sink secrets: %w", backup.ErrExportRead)
	}

	defer func() {
		_ = rows.Close() //nolint:errcheck // Read-only check handle.
	}()

	var out []sinkstore.Secret

	for rows.Next() {
		var (
			sec       sinkstore.Secret
			namespace sql.NullString
		)

		if scanErr := rows.Scan(&namespace, &sec.Ref, &sec.SinkID); scanErr != nil {
			return nil, fmt.Errorf("read staged sink secrets: %w", backup.ErrExportRead)
		}

		if !namespace.Valid {
			return nil, fmt.Errorf("staged sink secret %q has no namespace: %w", sec.Ref, backup.ErrCorrupt)
		}

		sec.Namespace = namespace.String
		out = append(out, sec)
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, fmt.Errorf("read staged sink secrets: %w", backup.ErrExportRead)
	}

	return out, nil
}

func stagedSinkRefs(ctx context.Context, conn *sql.DB) (map[string]bool, error) {
	const query = `SELECT n.name, r.ref
FROM notification_sink_refs AS r LEFT JOIN namespaces AS n ON n.id = r.namespace_id`

	rows, err := conn.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("read staged sink refs: %w", backup.ErrExportRead)
	}

	defer func() {
		_ = rows.Close() //nolint:errcheck // Read-only check handle.
	}()

	held := map[string]bool{}

	for rows.Next() {
		var (
			ref       string
			namespace sql.NullString
		)

		if scanErr := rows.Scan(&namespace, &ref); scanErr != nil {
			return nil, fmt.Errorf("read staged sink refs: %w", backup.ErrExportRead)
		}

		if !namespace.Valid {
			return nil, fmt.Errorf("staged sink ref %q has no namespace: %w", ref, backup.ErrCorrupt)
		}

		held[namespace.String+"/"+ref] = true
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, fmt.Errorf("read staged sink refs: %w", backup.ErrExportRead)
	}

	return held, nil
}

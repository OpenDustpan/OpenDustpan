package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/internal/jobstore"
)

// sqliteItemKeyBackupTables holds the backup columns of the item key table,
// in the order of backup.Tables.
func sqliteItemKeyBackupTables() []sqliteBackupTable {
	return []sqliteBackupTable{
		{
			table: "item_keys",
			columns: []string{
				"id", backupColNamespaceID, backupColScopeID, backupColKind, "item", backupColWrapperID,
				backupColCreatedAtMs, "shredded_at_ms", "blob_version", "blob_size", "blob_md5", "blob_attempt",
			},
		},
	}
}

// checkStagedOwnedRows checks the rows of a staged backup that belong to a
// namespace and carry a rule of their own: the saved views, the item keys and
// the removal alert markers.
func checkStagedOwnedRows(ctx context.Context, conn *sql.DB, restoreAt time.Time) error {
	if err := checkStagedSavedViews(ctx, conn); err != nil {
		return err
	}

	if err := checkStagedItemKeys(ctx, conn); err != nil {
		return err
	}

	if err := checkStagedRemovalAlerts(ctx, conn, restoreAt); err != nil {
		return err
	}

	return checkStagedJobArtifacts(ctx, conn)
}

// checkStagedItemKeys checks each item key row of a staged backup with the
// rules of a new record. A row that fails makes the backup corrupt, so a
// restore never brings in a record that the server would not have stored. It
// returns backup.ErrCorrupt.
//
// Defense in depth: the store checks a row again when it reads it. This check
// stays so that a bad row fails the restore and does not wait for a reader.
func checkStagedItemKeys(ctx context.Context, conn *sql.DB) error {
	const query = `SELECT k.id, n.name, k.scope_id, k.kind, k.item, k.wrapper_id, k.created_at_ms, k.shredded_at_ms,
	k.blob_version, k.blob_size, k.blob_md5, k.blob_attempt
FROM item_keys AS k LEFT JOIN namespaces AS n ON n.id = k.namespace_id`

	rows, err := conn.QueryContext(ctx, query)
	if err != nil {
		return fmt.Errorf("read staged item keys: %w", backup.ErrExportRead)
	}

	defer func() {
		_ = rows.Close() //nolint:errcheck // Read-only check handle.
	}()

	for rows.Next() {
		var (
			row       codec.ItemKeyRow
			namespace sql.NullString
		)

		scanErr := rows.Scan(&row.ID, &namespace, &row.ScopeID, &row.Kind, &row.Item, &row.WrapperID,
			&row.CreatedAtMs, &row.ShreddedAtMs, &row.BlobVersion, &row.BlobSize, &row.BlobMd5, &row.BlobAttempt)
		if scanErr != nil {
			return fmt.Errorf("read staged item keys: %w", backup.ErrExportRead)
		}

		// A record of a namespace that the backup does not hold is corrupt. The
		// LEFT JOIN keeps such a row in the result, and the NULL name fails it.
		if !namespace.Valid {
			return fmt.Errorf("staged item key %q has no namespace: %w", row.ID, backup.ErrCorrupt)
		}

		row.Namespace = namespace.String

		if _, keyErr := codec.FromItemKeyRow(row); keyErr != nil {
			return fmt.Errorf("staged item key %q is not valid: %w: %w", row.ID, keyErr, backup.ErrCorrupt)
		}
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		return fmt.Errorf("read staged item keys: %w", backup.ErrExportRead)
	}

	return nil
}

// checkStagedJobArtifacts checks the rows of plans, log versions and log chunks
// of a staged backup: each row names a namespace that the backup holds, each
// chunk row belongs to a log version of its namespace and has the rules of a
// reserve, and each plan body is the plan of its row. A row that fails makes the backup
// corrupt. It returns backup.ErrCorrupt.
//
// Defense in depth: the store checks a plan again when it reads it. This check
// stays so that a bad row fails the restore and does not wait for a reader.
func checkStagedJobArtifacts(ctx context.Context, conn *sql.DB) error {
	for _, table := range []string{"job_logs", "job_log_chunks"} {
		var orphans int64

		query := "SELECT COUNT(*) FROM " + table + " AS t LEFT JOIN namespaces AS n ON n.id = t.namespace_id " +
			"WHERE n.id IS NULL"
		if err := conn.QueryRowContext(ctx, query).Scan(&orphans); err != nil {
			return fmt.Errorf("read staged %s: %w", table, backup.ErrExportRead)
		}

		if orphans > 0 {
			return fmt.Errorf("staged %s has %d rows with no namespace: %w", table, orphans, backup.ErrCorrupt)
		}
	}

	if err := checkStagedLogChunks(ctx, conn); err != nil {
		return err
	}

	rows, err := conn.QueryContext(ctx, `SELECT p.id, n.id, p.body FROM job_plans AS p
LEFT JOIN namespaces AS n ON n.id = p.namespace_id`)
	if err != nil {
		return fmt.Errorf("read staged plans: %w", backup.ErrExportRead)
	}

	defer func() {
		_ = rows.Close() //nolint:errcheck // Read-only check handle.
	}()

	for rows.Next() {
		var (
			id, body string
			ns       sql.NullString
		)

		if scanErr := rows.Scan(&id, &ns, &body); scanErr != nil {
			return fmt.Errorf("read staged plans: %w", backup.ErrExportRead)
		}

		if !ns.Valid {
			return fmt.Errorf("staged plan %q has no namespace: %w", id, backup.ErrCorrupt)
		}

		if bodyErr := jobstore.CheckPlanBody(id, body); bodyErr != nil {
			return fmt.Errorf("staged plan %q is not valid: %w: %w", id, bodyErr, backup.ErrCorrupt)
		}
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		return fmt.Errorf("read staged plans: %w", backup.ErrExportRead)
	}

	return nil
}

// checkStagedLogChunks checks each log chunk row of a staged backup: the row
// names a log version of the same namespace, and its number, size and hash
// have the rules of a reserve. It returns backup.ErrCorrupt.
//
// Defense in depth: a reader checks the hash of a log again. This check stays
// so that a bad row fails the restore and does not wait for a reader.
func checkStagedLogChunks(ctx context.Context, conn *sql.DB) error {
	rows, err := conn.QueryContext(
		ctx,
		`SELECT c.log_id, c.seq, c.sha256, c.size, l.id, l.job_id, c.blob_key, c.confirmed = 1 FROM job_log_chunks AS c
LEFT JOIN job_logs AS l ON l.id = c.log_id AND l.namespace_id = c.namespace_id`,
	)
	if err != nil {
		return fmt.Errorf("read staged log chunks: %w", backup.ErrExportRead)
	}

	defer func() {
		_ = rows.Close() //nolint:errcheck // Read-only check handle.
	}()

	for rows.Next() {
		var (
			logID, sha, blobKey string
			seq, size           int64
			log, jobID          sql.NullString
			confirmed           bool
		)

		if scanErr := rows.Scan(&logID, &seq, &sha, &size, &log, &jobID, &blobKey, &confirmed); scanErr != nil {
			return fmt.Errorf("read staged log chunks: %w", backup.ErrExportRead)
		}

		if !log.Valid || !jobID.Valid {
			return fmt.Errorf("staged chunk %d of log %q has no log version: %w", seq, logID, backup.ErrCorrupt)
		}

		check := jobstore.ChunkRowCheck{
			Job: jobID.String, Log: logID, Seq: seq, Size: size, SHA256: sha, Confirmed: confirmed, BlobKey: blobKey,
		}

		if chunkErr := jobstore.CheckChunkRow(&check); chunkErr != nil {
			return fmt.Errorf("staged chunk %d of log %q is not valid: %w: %w", seq, logID, chunkErr, backup.ErrCorrupt)
		}
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		return fmt.Errorf("read staged log chunks: %w", backup.ErrExportRead)
	}

	return nil
}

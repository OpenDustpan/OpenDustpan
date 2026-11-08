package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/internal/jobstore"
)

// sqliteJobBackupTables holds the backup columns of the tables of jobs,
// change freezes and the state records that the job rules read, in the order
// of backup.Tables.
func sqliteJobBackupTables() []sqliteBackupTable {
	return []sqliteBackupTable{
		{
			table: "jobs",
			columns: []string{
				"id",
				backupColNamespaceID,
				backupColUnitID,
				backupColSeq,
				"state",
				"wait",
				"current_phase",
				"current_changes",
				"current_operation",
				"idempotency_key",
				backupColVersion,
				"body",
				backupColCreatedAtMs,
				backupColUpdatedAtMs,
				"state_changed_at_ms",
			},
		},
		{
			table: "job_freezes",
			columns: []string{
				"id",
				backupColNamespaceID,
				backupColReason,
				"starts_at_ms",
				"ends_at_ms",
				backupColCreatedBy,
				"ended_by",
			},
		},
		{
			table:   "unit_state_versions",
			columns: []string{"unit_id", backupColNamespaceID, "version_id", backupColUpdatedAtMs},
		},
		{
			table: "unit_final_versions",
			columns: []string{
				"unit_id",
				backupColNamespaceID,
				"version_id",
				"commit_id",
				backupColJobID,
				backupColAtMs,
			},
		},
		{
			table:   "job_plans",
			columns: []string{"id", backupColNamespaceID, backupColJobID, "body", backupColCreatedAtMs},
		},
		{
			table: "job_logs",
			columns: []string{
				"id", backupColNamespaceID, backupColJobID, "phase", "step", "token", backupColCreatedAtMs,
			},
		},
		{
			table:   "job_log_chunks",
			columns: sqliteJobChunkColumns(),
		},
	}
}

// sqliteJobChunkColumns returns the backup columns of the log chunk table. The
// last three hold the size and the MD5 of the stored bytes of the chunk blob
// and the key of that blob.
func sqliteJobChunkColumns() []string {
	return []string{
		"log_id", "seq", backupColNamespaceID, "sha256", "size", "confirmed", "stored_size", "stored_md5",
		backupColBlobKey,
	}
}

// checkStagedJobs checks each job row of a staged backup with the rules of a
// stored job: its body decodes, and it names the namespace and the unit of its
// row. A row that fails makes the backup corrupt, so a restore never brings in
// a job that the server would not have stored. It returns backup.ErrCorrupt.
//
// Defense in depth: the job store checks a row again when it reads it. This
// check stays so that a bad row fails the restore and does not wait for a
// reader.
func checkStagedJobs(ctx context.Context, conn *sql.DB) error {
	const query = `SELECT j.id, n.name, j.unit_id, j.seq, j.state, j.wait, j.current_phase, j.current_changes,
j.current_operation, j.idempotency_key, j.version, j.body, j.created_at_ms, j.updated_at_ms, j.state_changed_at_ms
FROM jobs AS j LEFT JOIN namespaces AS n ON n.id = j.namespace_id`

	rows, err := conn.QueryContext(ctx, query)
	if err != nil {
		return fmt.Errorf("read staged jobs: %w", backup.ErrExportRead)
	}

	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			_ = closeErr
		}
	}()

	for rows.Next() {
		row, scanErr := scanStagedJob(rows)
		if scanErr != nil {
			return scanErr
		}

		if _, jobErr := jobstore.Decode(&row); jobErr != nil {
			return fmt.Errorf("staged job %s is damaged: %s: %w", row.ID, jobstore.DamageReason(jobErr),
				backup.ErrCorrupt)
		}
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		return fmt.Errorf("read staged jobs: %w", backup.ErrExportRead)
	}

	return nil
}

// scanStagedJob reads the current row of rows. The second column is the name
// of the namespace, which is NULL when the backup does not hold the namespace
// of the job.
func scanStagedJob(rows *sql.Rows) (jobstore.Row, error) {
	// An error returns the zero row, so that no caller reads a partial job
	// from it.
	var (
		row  jobstore.Row
		none jobstore.Row
		name sql.NullString
	)

	scanErr := rows.Scan(
		&row.ID, &name, &row.UnitID, &row.Seq, &row.State, &row.Wait, &row.Phase, &row.Changes, &row.Operation,
		&row.Key, &row.Version, &row.Body, &row.CreatedMs, &row.UpdatedMs, &row.StateChangedMs,
	)
	if scanErr != nil {
		return none, fmt.Errorf("read staged jobs: %w", backup.ErrExportRead)
	}

	// A job of a namespace that the backup does not hold is corrupt. The
	// LEFT JOIN keeps such a row in the result, and the NULL name fails it.
	if !name.Valid {
		return none, fmt.Errorf("staged job %s has no namespace: %w", row.ID, backup.ErrCorrupt)
	}

	row.Namespace = namespace.Name(name.String)

	return row, nil
}

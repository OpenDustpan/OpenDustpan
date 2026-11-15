package postgres

import (
	"context"
	"fmt"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/internal/jobstore"
)

// pgCopyJobTables holds the COPY columns of the tables of jobs, change
// freezes and the state records that the job rules read, in the order of
// backup.Tables.
func pgCopyJobTables() []pgCopyTable {
	return []pgCopyTable{
		{
			table: "jobs",
			columns: []string{
				"id",
				pgColNamespaceID,
				pgColUnitID,
				pgColSeq,
				"state",
				"wait",
				"current_phase",
				"current_changes",
				"current_operation",
				"idempotency_key",
				pgColVersion,
				"body",
				pgColCreatedAtMs,
				pgColUpdatedAtMs,
				"state_changed_at_ms",
			},
			orderBy: []string{pgColSeq, "id"},
		},
		{
			table: "job_freezes",
			columns: []string{
				"id", pgColNamespaceID, pgColReason, "starts_at_ms", "ends_at_ms", pgColCreatedBy, "ended_by",
			},
			orderBy: []string{"id"},
		},
		{
			table:   "job_plans",
			columns: []string{"id", pgColNamespaceID, pgColJobID, "body", pgColCreatedAtMs},
			orderBy: []string{"id"},
		},
		{
			table:   "job_logs",
			columns: []string{"id", pgColNamespaceID, pgColJobID, "phase", "step", "token", pgColCreatedAtMs},
			orderBy: []string{"id"},
		},
		{
			table: "job_log_chunks",
			columns: []string{
				"log_id",
				pgColSeq,
				pgColNamespaceID,
				pgColSHA256,
				"size",
				"confirmed",
				"stored_size",
				"stored_md5",
				pgColBlobKey,
			},
			orderBy: []string{"log_id", pgColSeq},
		},
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
func checkStagedJobs(ctx context.Context, pool pgQuerier, staging string) error {
	query := "SELECT j.id, n.name, j.unit_id, j.seq, j.state, j.wait, j.current_phase, j.current_changes, " +
		"j.current_operation, j.idempotency_key, j.version, j.body, j.created_at_ms, j.updated_at_ms, " +
		"j.state_changed_at_ms FROM " + stagingTable(staging, "jobs") + " AS j LEFT JOIN " +
		stagingTable(staging, "namespaces") + " AS n ON n.id = j.namespace_id"

	rows, err := pool.Query(ctx, query)
	if err != nil {
		return fmt.Errorf("read staged jobs: %w", backup.ErrExportRead)
	}

	defer rows.Close()

	for rows.Next() {
		var (
			row  jobstore.Row
			name *string
		)

		scanErr := rows.Scan(&row.ID, &name, &row.UnitID, &row.Seq, &row.State, &row.Wait, &row.Phase,
			&row.Changes, &row.Operation, &row.Key, &row.Version, &row.Body, &row.CreatedMs, &row.UpdatedMs,
			&row.StateChangedMs)
		if scanErr != nil {
			return fmt.Errorf("read staged jobs: %w", backup.ErrExportRead)
		}

		// A job of a namespace that the backup does not hold is corrupt. The
		// LEFT JOIN keeps such a row in the result, and the NULL name fails it.
		if name == nil {
			return fmt.Errorf("staged job %s has no namespace: %w", row.ID, backup.ErrCorrupt)
		}

		row.Namespace = namespace.Name(*name)

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

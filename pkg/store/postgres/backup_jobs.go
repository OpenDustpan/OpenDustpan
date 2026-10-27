package postgres

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
			table:   "unit_state_versions",
			columns: []string{pgColUnitID, pgColNamespaceID, "version_id", pgColUpdatedAtMs},
			orderBy: []string{pgColUnitID},
		},
		{
			table:   "unit_final_versions",
			columns: []string{pgColUnitID, pgColNamespaceID, "version_id", "commit_id", pgColJobID, pgColAtMs},
			orderBy: []string{pgColUnitID},
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
			},
			orderBy: []string{"log_id", pgColSeq},
		},
	}
}

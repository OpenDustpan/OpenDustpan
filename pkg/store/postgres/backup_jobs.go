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
				"seq",
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
			},
			orderBy: []string{"seq", "id"},
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
	}
}

package sqlite

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
	}
}

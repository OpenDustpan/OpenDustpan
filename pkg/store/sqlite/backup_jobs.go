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

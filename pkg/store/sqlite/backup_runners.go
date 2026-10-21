package sqlite

// Column names that more than one runner table holds.
const (
	backupColSHA256      = "sha256"
	backupColRevokedAtMs = "revoked_at_ms"
	backupColOperationID = "operation_id"
	backupColToken       = "token"
)

// sqliteRunnerBackupTables holds the backup columns of the tables of runner
// pools, join tokens and runners, in the order of backup.Tables.
func sqliteRunnerBackupTables() []sqliteBackupTable {
	return []sqliteBackupTable{
		{
			table: "runner_pools",
			columns: []string{
				"id",
				backupColNamespaceID,
				backupColName,
				"purpose",
				"lease_ms",
				"concurrency",
				"isolation",
				"git_mode",
				"git_helper",
				backupColCreatedAtMs,
				backupColUpdatedAtMs,
			},
		},
		{
			table: "runner_join_tokens",
			columns: []string{
				"id",
				backupColNamespaceID,
				"pool_id",
				backupColSHA256,
				backupColCreatedBy,
				backupColCreatedAtMs,
				backupColExpiresAtMs,
				"used_at_ms",
				"used_by",
				backupColRevokedAtMs,
			},
		},
		{
			table: "runners",
			columns: []string{
				"id",
				backupColNamespaceID,
				"pool_id",
				backupColName,
				backupColKind,
				"isolation",
				"labels",
				"release",
				"key_public",
				backupColKeyCreatedAtMs,
				"previous_key_public",
				"previous_key_created_at_ms",
				"rotated_at_ms",
				"joined_with",
				"draining",
				"retired_at_ms",
				"last_seen_at_ms",
				"joined_at_ms",
			},
		},
	}
}

// sqliteRunnerControlTables lists the column lists of the control tables of
// the runner protocol.
func sqliteRunnerControlTables() []sqliteBackupTable {
	return []sqliteBackupTable{
		{
			table:   "runner_claims",
			columns: []string{backupColOperationID, backupColToken, "claimed_at_ms", backupColJobID, "phase"},
		},
		{
			table: "runner_handoff_tokens",
			columns: []string{
				backupColSHA256,
				backupColOperationID,
				backupColToken,
				"runner_id",
				"issued_at_ms",
				backupColExpiresAtMs,
				"used_at_ms",
			},
		},
		{
			table: "runner_sessions",
			columns: []string{
				"id",
				backupColOperationID,
				backupColToken,
				"runner_id",
				"key_public",
				backupColKeyCreatedAtMs,
				backupColCreatedAtMs,
			},
		},
		{table: "runner_nonces", columns: []string{"subject", "nonce", backupColExpiresAtMs}},
	}
}

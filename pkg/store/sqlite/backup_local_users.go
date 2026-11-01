package sqlite

// sqliteLocalUserBackupTables holds the backup columns of the table of local
// users, in the order of backup.Tables.
func sqliteLocalUserBackupTables() []sqliteBackupTable {
	return []sqliteBackupTable{
		{
			table: "local_users",
			columns: []string{
				"id", backupColPrincipalID, "user_name", "password_hash", "password_set_at_ms", "change_required",
				"demo", backupColCreatedBy, backupColCreatedAtMs,
			},
		},
	}
}

// sqliteLocalUserResetColumns holds the columns of the table of reset codes,
// which a backup leaves out.
func sqliteLocalUserResetColumns() []string {
	return []string{
		"id", "user_id", backupColPrincipalID, "digest", "purpose", backupColCreatedBy, backupColReason,
		"revoke_access_tokens", backupColCreatedAtMs, "expires_at_ms",
	}
}

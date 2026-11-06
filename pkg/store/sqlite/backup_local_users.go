package sqlite

// sqliteLocalUserBackupTables holds the backup columns of the table of local
// users, of the table of session generations and of the table of passkeys, in
// the order of backup.Tables.
func sqliteLocalUserBackupTables() []sqliteBackupTable {
	return []sqliteBackupTable{
		{
			table: "local_users",
			columns: []string{
				"id", backupColPrincipalID, "user_name", "password_hash", "password_set_at_ms", "change_required",
				"demo", backupColCreatedBy, backupColCreatedAtMs,
			},
		},
		{
			table:   "principal_session_generations",
			columns: []string{backupColPrincipalID, "generation"},
		},
		{
			table: "passkeys",
			columns: []string{
				"id",
				backupColPrincipalID,
				backupColName,
				"credential_id",
				"public_key",
				"transports",
				"aaguid",
				"sign_count",
				"backup_eligible",
				backupColCreatedAtMs,
				"last_used_at_ms",
				"suspended_at_ms",
			},
		},
	}
}

// sqlitePasskeyChallengeColumns holds the columns of the table of open
// passkey ceremonies, which a backup leaves out.
func sqlitePasskeyChallengeColumns() []string {
	return []string{
		backupColDigest, backupColKind, backupColPrincipalID, "pending_id", "session_id", backupColCreatedAtMs,
		"expires_at_ms",
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

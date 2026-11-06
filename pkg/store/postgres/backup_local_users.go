package postgres

// pgCopyLocalUserTables holds the COPY columns of the table of local users,
// of the table of session generations and of the table of passkeys, in the
// order of backup.Tables.
func pgCopyLocalUserTables() []pgCopyTable {
	return []pgCopyTable{
		{
			table: "local_users",
			columns: []string{
				"id", pgColPrincipalID, "user_name", "password_hash", "password_set_at_ms", "change_required", "demo",
				pgColCreatedBy, pgColCreatedAtMs,
			},
			orderBy: []string{"id"},
		},
		{
			table:   "principal_session_generations",
			columns: []string{pgColPrincipalID, "generation"},
			orderBy: []string{pgColPrincipalID},
		},
		{
			table: "passkeys",
			columns: []string{
				"id", pgColPrincipalID, pgColName, "credential_id", "public_key", "transports", "aaguid", "sign_count",
				"backup_eligible", pgColCreatedAtMs, "last_used_at_ms", "suspended_at_ms",
			},
			orderBy: []string{"id"},
		},
	}
}

// pgPasskeyChallengeColumns holds the columns of the table of open passkey
// ceremonies, which a backup leaves out.
func pgPasskeyChallengeColumns() []string {
	return []string{
		pgColDigest, pgColKind, pgColPrincipalID, "pending_id", "session_id", pgColSource, "source_group",
		pgColCreatedAtMs, "expires_at_ms",
	}
}

// pgLocalUserResetColumns holds the columns of the table of reset codes, which
// a backup leaves out.
func pgLocalUserResetColumns() []string {
	return []string{
		"id", "user_id", pgColPrincipalID, "digest", "purpose", pgColCreatedBy, "reason", "revoke_access_tokens",
		pgColCreatedAtMs, "expires_at_ms",
	}
}

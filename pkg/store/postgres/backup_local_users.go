package postgres

// pgCopyLocalUserTables holds the COPY columns of the table of local users,
// in the order of backup.Tables.
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

package postgres

import "opendustpan.org/opendustpan/pkg/backup"

// pgCopyFirstAdministratorTables holds the COPY columns of the marker "first
// administrator made", in the order of backup.Tables.
func pgCopyFirstAdministratorTables() []pgCopyTable {
	return []pgCopyTable{
		{
			table: backup.TableFirstAdministrator,
			columns: []string{
				"id", pgColPrincipalID, "grant_id", "session_id", pgColCreatedBy, "made_at_ms",
			},
			orderBy: []string{"id"},
		},
	}
}

// pgFirstAdministratorCodeColumns lists the columns of the table of the open
// code. The table is emptied: a backup leaves it out and a restore empties it.
func pgFirstAdministratorCodeColumns() []string {
	return []string{
		"id", pgColPrincipalID, pgColDigest, pgColCreatedBy, pgColReason, pgColCreatedAtMs, pgColExpiresAtMs,
	}
}

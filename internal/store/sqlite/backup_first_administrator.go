package sqlite

import "opendustpan.org/opendustpan/pkg/backup"

// sqliteFirstAdministratorBackupTables holds the backup columns of the marker
// "first administrator made", in the order of backup.Tables.
func sqliteFirstAdministratorBackupTables() []sqliteBackupTable {
	return []sqliteBackupTable{
		{
			table: backup.TableFirstAdministrator,
			columns: []string{
				"id", backupColPrincipalID, "grant_id", "session_id", backupColCreatedBy, "made_at_ms",
			},
		},
	}
}

// sqliteFirstAdministratorCodeColumns lists the columns of the table of the
// open code. The table is emptied: a backup leaves it out and a restore empties
// it.
func sqliteFirstAdministratorCodeColumns() []string {
	return []string{
		"id", backupColPrincipalID, backupColDigest, backupColCreatedBy, backupColReason,
		backupColCreatedAtMs, backupColExpiresAtMs,
	}
}

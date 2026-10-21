package sqlite

// sqlitePreferenceBackupTables holds the backup columns of the table of the
// preferences of a person, in the order of backup.Tables.
func sqlitePreferenceBackupTables() []sqliteBackupTable {
	return []sqliteBackupTable{
		{
			table:   "person_preferences",
			columns: []string{"principal_id", "document", backupColUpdatedAtMs},
		},
	}
}

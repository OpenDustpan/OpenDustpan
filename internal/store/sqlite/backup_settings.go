package sqlite

// The columns that the tables of the settings share with other backup tables.
const (
	backupColScopeID  = "scope_id"
	backupColRevision = "revision"
)

// sqliteSettingBackupTables holds the backup columns of the tables of the
// database layer of the settings, in the order of backup.Tables.
func sqliteSettingBackupTables() []sqliteBackupTable {
	return []sqliteBackupTable{
		{
			table:   "settings",
			columns: []string{backupColScopeID, "key", "value", backupColUpdatedAtMs},
		},
		{
			table:   "settings_state",
			columns: []string{backupColScopeID, backupColRevision, "static_digest"},
		},
	}
}

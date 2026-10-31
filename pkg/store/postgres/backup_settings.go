package postgres

// pgColRevision is the column that holds the revision of a row.
const pgColRevision = "revision"

// pgCopySettingTables holds the COPY columns of the tables of the database
// layer of the settings, in the order of backup.Tables.
func pgCopySettingTables() []pgCopyTable {
	return []pgCopyTable{
		{
			table:   "settings",
			columns: []string{pgColScopeID, "key", "value", pgColUpdatedAtMs},
			orderBy: []string{pgColScopeID, "key"},
		},
		{
			table:   "settings_state",
			columns: []string{pgColScopeID, pgColRevision, "static_digest"},
			orderBy: []string{pgColScopeID},
		},
	}
}

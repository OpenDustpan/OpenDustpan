package postgres

// pgCopyPreferenceTables holds the COPY columns of the table of the
// preferences of a person, in the order of backup.Tables.
func pgCopyPreferenceTables() []pgCopyTable {
	return []pgCopyTable{
		{
			table:   "person_preferences",
			columns: []string{"principal_id", "document", pgColUpdatedAtMs},
			orderBy: []string{"principal_id"},
		},
	}
}

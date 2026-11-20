package postgres

// pgColHolder is the column that holds the subject of the holder of a lock.
const pgColHolder = "holder"

// pgCopyManualLockTables holds the COPY columns of the manual lock table, in
// the order of backup.Tables.
func pgCopyManualLockTables() []pgCopyTable {
	return []pgCopyTable{
		{
			table:   "manual_locks",
			columns: []string{pgColKind, pgColObjectID, pgColNamespace, pgColHolder, pgColReason, "since_ms"},
			orderBy: []string{pgColKind, pgColObjectID},
		},
	}
}

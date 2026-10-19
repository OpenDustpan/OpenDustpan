package sqlite

// backupColHolder is the column that holds the subject of the holder of a lock.
const backupColHolder = "holder"

// sqliteManualLockBackupTables holds the backup columns of the manual lock
// table, in the order of backup.Tables.
func sqliteManualLockBackupTables() []sqliteBackupTable {
	return []sqliteBackupTable{
		{
			table: "manual_locks",
			columns: []string{
				backupColKind, backupColObjectID, backupColNamespace, backupColHolder, backupColReason, "since_ms",
			},
		},
	}
}

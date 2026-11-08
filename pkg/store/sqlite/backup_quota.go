package sqlite

// Column names of the tables of the quota counter.
const (
	backupColBlueprint     = "blueprint"
	backupColScope         = "scope"
	backupColReservationID = "reservation_id"
)

// sqliteQuotaBackupTables holds the backup columns of the tables of the quota
// counter, in the order of backup.Tables.
func sqliteQuotaBackupTables() []sqliteBackupTable {
	return []sqliteBackupTable{
		{
			table: "quota_keys",
			columns: []string{
				backupColNamespaceID, backupColBlueprint, backupColKind, backupColScope, "settled_count",
			},
		},
		{
			table:   "quota_reservations",
			columns: []string{backupColNamespaceID, "id", backupColState, backupColAtMs, "ends_at_ms"},
		},
		{
			table: "quota_reservation_keys",
			columns: []string{
				backupColNamespaceID, backupColReservationID, backupColBlueprint, backupColKind, backupColScope,
			},
		},
		{
			table: "quota_requests",
			columns: []string{
				"id", backupColNamespaceID, backupColBlueprint, backupColKind, backupColScope, backupColAtMs,
			},
		},
	}
}

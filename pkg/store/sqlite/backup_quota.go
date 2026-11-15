package sqlite

// Column names of the quota tables that more than one of them holds.
const (
	backupColBlueprintID   = "blueprint_id"
	backupColReservationID = "reservation_id"
	backupColScope         = "scope"
	backupColSettled       = "settled"
	backupColSubmittedAtMs = "submitted_at_ms"
	backupColEndsAtMs      = "ends_at_ms"
)

// sqliteQuotaBackupTables holds the backup columns of the quota tables, in the
// order of backup.Tables.
func sqliteQuotaBackupTables() []sqliteBackupTable {
	return []sqliteBackupTable{
		{
			table: "quota_keys",
			columns: []string{
				backupColNamespaceID, backupColBlueprintID, backupColKind, backupColScope, backupColSettled,
			},
		},
		{
			table: "quota_reservations",
			columns: []string{
				backupColNamespaceID, "id", backupColState, backupColAtMs, backupColEndsAtMs,
			},
		},
		{
			table: "quota_reservation_keys",
			columns: []string{
				backupColNamespaceID, backupColReservationID, backupColBlueprintID, backupColKind, backupColScope,
			},
		},
		{
			table: "quota_requests",
			columns: []string{
				backupColNamespaceID,
				backupColBlueprintID,
				backupColKind,
				backupColScope,
				backupColSubmittedAtMs,
				backupColSeq,
			},
		},
	}
}

package postgres

// Column names of the tables of the quota counter.
const (
	pgColBlueprint     = "blueprint"
	pgColScope         = "scope"
	pgColReservationID = "reservation_id"
)

// pgCopyQuotaTables holds the COPY columns of the tables of the quota counter,
// in the order of backup.Tables.
func pgCopyQuotaTables() []pgCopyTable {
	return []pgCopyTable{
		{
			table:   "quota_keys",
			columns: []string{pgColNamespaceID, pgColBlueprint, pgColKind, pgColScope, "settled_count"},
			orderBy: []string{pgColNamespaceID, pgColBlueprint, pgColKind, pgColScope},
		},
		{
			table:   "quota_reservations",
			columns: []string{pgColNamespaceID, "id", pgColState, pgColAtMs, "ends_at_ms"},
			orderBy: []string{pgColNamespaceID, "id"},
		},
		{
			table: "quota_reservation_keys",
			columns: []string{
				pgColNamespaceID, pgColReservationID, pgColBlueprint, pgColKind, pgColScope,
			},
			orderBy: []string{pgColNamespaceID, pgColReservationID, pgColBlueprint, pgColKind, pgColScope},
		},
		{
			table:   "quota_requests",
			columns: []string{"id", pgColNamespaceID, pgColBlueprint, pgColKind, pgColScope, pgColAtMs},
			orderBy: []string{"id"},
		},
	}
}

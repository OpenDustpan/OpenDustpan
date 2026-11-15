package postgres

// Column names of the quota tables that more than one of them holds.
const (
	pgColBlueprintID   = "blueprint_id"
	pgColReservationID = "reservation_id"
	pgColScope         = "scope"
	pgColSettled       = "settled"
	pgColSubmittedAtMs = "submitted_at_ms"
	pgColEndsAtMs      = "ends_at_ms"
)

// pgCopyQuotaTables holds the COPY columns of the quota tables, in the order of
// backup.Tables.
func pgCopyQuotaTables() []pgCopyTable {
	return []pgCopyTable{
		{
			table:   "quota_keys",
			columns: []string{pgColNamespaceID, pgColBlueprintID, pgColKind, pgColScope, pgColSettled},
			orderBy: []string{pgColNamespaceID, pgColBlueprintID, pgColKind, pgColScope},
		},
		{
			table:   "quota_reservations",
			columns: []string{pgColNamespaceID, "id", pgColState, pgColAtMs, pgColEndsAtMs},
			orderBy: []string{pgColNamespaceID, "id"},
		},
		{
			table: "quota_reservation_keys",
			columns: []string{
				pgColNamespaceID, pgColReservationID, pgColBlueprintID, pgColKind, pgColScope,
			},
			orderBy: []string{pgColNamespaceID, pgColReservationID, pgColBlueprintID, pgColKind, pgColScope},
		},
		{
			table: "quota_requests",
			columns: []string{
				pgColNamespaceID, pgColBlueprintID, pgColKind, pgColScope, pgColSubmittedAtMs, pgColSeq,
			},
			orderBy: []string{
				pgColNamespaceID, pgColBlueprintID, pgColKind, pgColScope, pgColSubmittedAtMs, pgColSeq,
			},
		},
	}
}

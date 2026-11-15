package backup

// Column names of the quota tables that more than one of them holds.
const (
	portColBlueprintID   = "blueprint_id"
	portColReservationID = "reservation_id"
	portColSubmittedAtMs = "submitted_at_ms"
	portColSettled       = "settled"
)

// portableQuotaTables is the portable description of the quota tables, in the
// order of Tables: the keys with their settled counts, the reservations, the
// keys that each reservation holds, and the requests of a window. The
// namespace and the blueprint are stored as their stable IDs, so a rename
// keeps the count.
func portableQuotaTables() []PortableTable {
	return []PortableTable{
		{
			Table:   "quota_keys",
			OrderBy: []string{portColNamespaceID, portColBlueprintID, portColKind, portColScope},
			Columns: []Column{
				{Name: portColNamespaceID, Kind: KindText},
				{Name: portColBlueprintID, Kind: KindText},
				{Name: portColKind, Kind: KindText},
				{Name: portColScope, Kind: KindText},
				{Name: portColSettled, Kind: KindInteger},
			},
		},
		{
			Table:   "quota_reservations",
			OrderBy: []string{portColNamespaceID, "id"},
			Columns: []Column{
				{Name: portColNamespaceID, Kind: KindText},
				{Name: "id", Kind: KindText},
				{Name: portColState, Kind: KindText},
				{Name: portColAtMs, Kind: KindTime},
				{Name: "ends_at_ms", Kind: KindTime},
			},
		},
		{
			Table:   "quota_reservation_keys",
			OrderBy: []string{portColNamespaceID, portColReservationID, portColBlueprintID, portColKind, portColScope},
			Columns: []Column{
				{Name: portColNamespaceID, Kind: KindText},
				{Name: portColReservationID, Kind: KindText},
				{Name: portColBlueprintID, Kind: KindText},
				{Name: portColKind, Kind: KindText},
				{Name: portColScope, Kind: KindText},
			},
		},
		{
			Table: "quota_requests",
			OrderBy: []string{
				portColNamespaceID, portColBlueprintID, portColKind, portColScope, portColSubmittedAtMs, portColSeq,
			},
			Columns: []Column{
				{Name: portColNamespaceID, Kind: KindText},
				{Name: portColBlueprintID, Kind: KindText},
				{Name: portColKind, Kind: KindText},
				{Name: portColScope, Kind: KindText},
				{Name: portColSubmittedAtMs, Kind: KindTime},
				{Name: portColSeq, Kind: KindInteger},
			},
		},
	}
}

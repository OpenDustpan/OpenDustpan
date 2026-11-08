package backup

// Column names that more than one table of the quota counter holds.
const (
	portColBlueprint     = "blueprint"
	portColReservationID = "reservation_id"
)

// portableQuotaTables is the portable description of the tables of the quota
// counter, in the order of Tables: the row of each key with its settled count,
// the reservations with their keys, and the recorded requests. The counter
// writes the times as Unix milliseconds, as the other tables do.
func portableQuotaTables() []PortableTable {
	return []PortableTable{
		{
			Table:   "quota_keys",
			OrderBy: []string{portColNamespaceID, portColBlueprint, portColKind, portColScope},
			Columns: []Column{
				{Name: portColNamespaceID, Kind: KindText},
				{Name: portColBlueprint, Kind: KindText},
				{Name: portColKind, Kind: KindText},
				{Name: portColScope, Kind: KindText},
				{Name: "settled_count", Kind: KindInteger},
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
			Table: "quota_reservation_keys",
			OrderBy: []string{
				portColNamespaceID,
				portColReservationID,
				portColBlueprint,
				portColKind,
				portColScope,
			},
			Columns: []Column{
				{Name: portColNamespaceID, Kind: KindText},
				{Name: portColReservationID, Kind: KindText},
				{Name: portColBlueprint, Kind: KindText},
				{Name: portColKind, Kind: KindText},
				{Name: portColScope, Kind: KindText},
			},
		},
		{
			Table:   "quota_requests",
			OrderBy: []string{"id"},
			Columns: []Column{
				{Name: "id", Kind: KindText},
				{Name: portColNamespaceID, Kind: KindText},
				{Name: portColBlueprint, Kind: KindText},
				{Name: portColKind, Kind: KindText},
				{Name: portColScope, Kind: KindText},
				{Name: portColAtMs, Kind: KindTime},
			},
		},
	}
}

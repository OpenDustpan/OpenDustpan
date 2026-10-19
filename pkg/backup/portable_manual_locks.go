package backup

// Column names that the manual lock table shares with other tables.
const (
	portColObjectIDOfLock = "object_id"
	portColHolder         = "holder"
)

// portableManualLockTables is the portable description of the manual lock
// table, in the order of Tables.
func portableManualLockTables() []PortableTable {
	return []PortableTable{
		{
			Table:   "manual_locks",
			OrderBy: []string{portColKind, portColObjectIDOfLock},
			Columns: []Column{
				{Name: portColKind, Kind: KindText},
				{Name: portColObjectIDOfLock, Kind: KindText},
				{Name: portColNamespace, Kind: KindText},
				{Name: portColHolder, Kind: KindText},
				{Name: portColReason, Kind: KindText},
				{Name: "since_ms", Kind: KindTime},
			},
		},
	}
}

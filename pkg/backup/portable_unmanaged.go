package backup

// portableUnmanagedTables is the portable description of the tables of the
// sweeps, in the order of Tables: the sweep targets, then the sweeps. A sweep
// keeps the name of the target that started it, so it names no target row.
func portableUnmanagedTables() []PortableTable {
	return []PortableTable{
		{
			Table:   "unmanaged_targets",
			OrderBy: []string{portColNamespaceID, portColName},
			Columns: []Column{
				{Name: portColNamespaceID, Kind: KindText},
				{Name: portColName, Kind: KindText},
				{Name: portColPoolID, Kind: KindText},
				{Name: "scopes_json", Kind: KindJSON},
				{Name: "command_json", Kind: KindJSON},
				{Name: "every_ms", Kind: KindInteger},
				{Name: "timeout_ms", Kind: KindInteger},
				{Name: portColEnabled, Kind: KindBool},
			},
		},
		{
			Table:   "unmanaged_scans",
			OrderBy: []string{"id"},
			Columns: []Column{
				{Name: "id", Kind: KindText},
				{Name: portColNamespaceID, Kind: KindText},
				{Name: portColSeq, Kind: KindInteger},
				{Name: portColTarget, Kind: KindText},
				{Name: portColKind, Kind: KindText},
				{Name: portColStatus, Kind: KindText},
				{Name: "started_by", Kind: KindText},
				{Name: portColReason, Kind: KindText},
				{Name: "scopes_json", Kind: KindJSON},
				{Name: "counts_json", Kind: KindJSON},
				{Name: portColCreatedAtMs, Kind: KindTime},
				{Name: portColEndedAtMs, Kind: KindTime},
			},
		},
	}
}

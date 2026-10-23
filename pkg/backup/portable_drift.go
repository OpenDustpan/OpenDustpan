package backup

// portableDriftTables is the portable description of the drift result table,
// in the order of Tables.
func portableDriftTables() []PortableTable {
	return []PortableTable{
		{
			Table:   "drift_results",
			OrderBy: []string{portColUnitID},
			Columns: []Column{
				{Name: portColUnitID, Kind: KindText},
				{Name: portColNamespaceID, Kind: KindText},
				{Name: portColTool, Kind: KindText},
				{Name: portColJobID, Kind: KindText},
				{Name: portColStatus, Kind: KindText},
				{Name: "checked_at_ms", Kind: KindTime},
				{Name: "truncated", Kind: KindBool},
				{Name: "resources_json", Kind: KindText},
			},
		},
	}
}

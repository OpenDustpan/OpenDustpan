package backup

// portableSettingTables is the portable description of the tables of the
// database layer of the settings, in the order of Tables.
func portableSettingTables() []PortableTable {
	return []PortableTable{
		{
			Table:   "settings",
			OrderBy: []string{portColScopeID, "key"},
			Columns: []Column{
				{Name: portColScopeID, Kind: KindText},
				{Name: "key", Kind: KindText},
				{Name: "value", Kind: KindText},
				{Name: portColUpdatedAtMs, Kind: KindTime},
			},
		},
		{
			Table:   "settings_state",
			OrderBy: []string{portColScopeID},
			Columns: []Column{
				{Name: portColScopeID, Kind: KindText},
				{Name: portColRevision, Kind: KindInteger},
				{Name: "static_digest", Kind: KindText},
			},
		},
	}
}

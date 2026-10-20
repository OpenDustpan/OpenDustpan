package backup

// portableSavedViewTables is the portable description of the saved view
// table, in the order of Tables.
func portableSavedViewTables() []PortableTable {
	return []PortableTable{
		{
			Table:   "saved_views",
			OrderBy: []string{"id"},
			Columns: []Column{
				{Name: "id", Kind: KindText},
				{Name: portColNamespaceID, Kind: KindText},
				{Name: portColOwner, Kind: KindText},
				{Name: portColName, Kind: KindText},
				{Name: "query_json", Kind: KindText},
				{Name: portColCreatedAtMs, Kind: KindTime},
			},
		},
	}
}

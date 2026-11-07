package backup

// portableItemKeyTables is the portable description of the item key table,
// in the order of Tables. A record holds no key: the wrapped key is a blob
// that a backup copies like every other blob.
func portableItemKeyTables() []PortableTable {
	return []PortableTable{
		{
			Table:   "item_keys",
			OrderBy: []string{"id"},
			Columns: []Column{
				{Name: "id", Kind: KindText},
				{Name: portColNamespaceID, Kind: KindText},
				{Name: portColScopeID, Kind: KindText},
				{Name: portColKind, Kind: KindText},
				{Name: "item", Kind: KindText},
				{Name: portColWrapperID, Kind: KindText},
				{Name: portColCreatedAtMs, Kind: KindTime},
				{Name: "shredded_at_ms", Kind: KindInteger},
				{Name: "blob_version", Kind: KindInteger},
				{Name: "blob_size", Kind: KindInteger},
				{Name: "blob_md5", Kind: KindBytes},
				{Name: "blob_attempt", Kind: KindText},
			},
		},
	}
}

package backup

// portableProfileTables is the portable description of the profile tables
// (profiles and profile_revisions), in the order of Tables.
func portableProfileTables() []PortableTable {
	return []PortableTable{
		{
			Table:   "profiles",
			OrderBy: []string{portColNamespaceID, "id"},
			Columns: []Column{
				{Name: portColNamespaceID, Kind: KindText},
				{Name: "id", Kind: KindText},
				{Name: "name", Kind: KindText},
				{Name: "latest", Kind: KindInteger},
				{Name: "created_by", Kind: KindText},
				{Name: portColCreatedAtMs, Kind: KindTime},
				{Name: "updated_at_ms", Kind: KindTime},
				{Name: "deleted_at_ms", Kind: KindTime},
			},
		},
		{
			Table:   "profile_revisions",
			OrderBy: []string{portColNamespaceID, "profile_id", "number"},
			Columns: []Column{
				{Name: portColNamespaceID, Kind: KindText},
				{Name: "profile_id", Kind: KindText},
				{Name: "number", Kind: KindInteger},
				{Name: "document", Kind: KindText},
				{Name: "document_sha256", Kind: KindText},
				{Name: "author", Kind: KindText},
				{Name: portColCreatedAtMs, Kind: KindTime},
				{Name: "reason", Kind: KindText},
			},
		},
	}
}

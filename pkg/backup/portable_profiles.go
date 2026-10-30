package backup

// portableProfileTables is the portable description of the profile tables
// (profiles and profile_revisions) and of the tables of the issuer
// (issuer_keys and issuer_settings), in the order of Tables.
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
				{Name: portColCreatedBy, Kind: KindText},
				{Name: portColCreatedAtMs, Kind: KindTime},
				{Name: portColUpdatedAtMs, Kind: KindTime},
				{Name: "deleted_at_ms", Kind: KindTime},
			},
		},
		{
			Table:   "profile_revisions",
			OrderBy: []string{portColNamespaceID, "profile_id", portColNumber},
			Columns: []Column{
				{Name: portColNamespaceID, Kind: KindText},
				{Name: "profile_id", Kind: KindText},
				{Name: portColNumber, Kind: KindInteger},
				{Name: "document", Kind: KindText},
				{Name: "document_sha256", Kind: KindText},
				{Name: "author", Kind: KindText},
				{Name: portColCreatedAtMs, Kind: KindTime},
				{Name: portColReason, Kind: KindText},
			},
		},
		{
			Table:   "issuer_keys",
			OrderBy: []string{portColNamespaceID, "id"},
			Columns: []Column{
				{Name: portColNamespaceID, Kind: KindText},
				{Name: "id", Kind: KindText},
				{Name: "state", Kind: KindText},
				{Name: "public_key", Kind: KindBytes},
				{Name: "sealed", Kind: KindBytes},
				{Name: portColCreatedAtMs, Kind: KindTime},
				{Name: "activated_at_ms", Kind: KindTime},
				{Name: "retired_at_ms", Kind: KindTime},
				{Name: "deleted_at_ms", Kind: KindTime},
			},
		},
		{
			Table:   "issuer_settings",
			OrderBy: []string{portColNamespaceID},
			Columns: []Column{
				{Name: portColNamespaceID, Kind: KindText},
				{Name: "issuer_url", Kind: KindText},
				{Name: "subject_template", Kind: KindText},
				{Name: "allow_block_subjects", Kind: KindBool},
				{Name: "include_actor_name", Kind: KindBool},
				{Name: portColVersion, Kind: KindInteger},
				{Name: portColUpdatedAtMs, Kind: KindTime},
				{Name: "updated_by", Kind: KindText},
				{Name: "copy_check", Kind: KindText},
			},
		},
	}
}

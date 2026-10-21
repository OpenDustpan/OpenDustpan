package backup

// portColAddedAtMs is the column that holds the time a trusted key was registered.
const portColAddedAtMs = "added_at_ms"

// portableRegistryTables is the portable description of the registry release
// table, in the order of Tables.
func portableRegistryTables() []PortableTable {
	return []PortableTable{
		{
			Table:   "registry_releases",
			OrderBy: []string{portColNamespaceID, portColKind, portColName, "system", portColVersion},
			Columns: []Column{
				{Name: portColNamespaceID, Kind: KindText},
				{Name: portColKind, Kind: KindText},
				{Name: portColName, Kind: KindText},
				{Name: "system", Kind: KindText},
				{Name: portColVersion, Kind: KindText},
				{Name: "sums_file", Kind: KindText},
				{Name: "sums_sha256", Kind: KindText},
				{Name: "signature_file", Kind: KindText},
				{Name: "signature_sha256", Kind: KindText},
				{Name: "key_fingerprint", Kind: KindText},
				{Name: "protocols", Kind: KindText},
				{Name: "published_by", Kind: KindText},
				{Name: "archives_json", Kind: KindText},
				{Name: "published_at_ms", Kind: KindTime},
			},
		},
		{
			Table:   "registry_keys",
			OrderBy: []string{portColNamespaceID, "fingerprint"},
			Columns: []Column{
				{Name: portColNamespaceID, Kind: KindText},
				{Name: "fingerprint", Kind: KindText},
				{Name: "armored_key", Kind: KindText},
				{Name: "added_by", Kind: KindText},
				{Name: portColAddedAtMs, Kind: KindTime},
			},
		},
	}
}

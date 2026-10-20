package backup

// portColImportID is the column that names an import in the import tables.
const portColImportID = "import_id"

// portableImportTables is the portable description of the tables of the
// import tool (imports, imports_purged, import_units and import_ledger), in
// the order of Tables.
func portableImportTables() []PortableTable {
	return append(portableImportRecords(), portableImportUnits()...)
}

// portableImportRecords describes imports and imports_purged.
func portableImportRecords() []PortableTable {
	return []PortableTable{
		{
			Table:   "imports",
			OrderBy: []string{portColNamespaceID, "id"},
			Columns: []Column{
				{Name: portColNamespaceID, Kind: KindText},
				{Name: "id", Kind: KindText},
				{Name: "kind", Kind: KindText},
				{Name: portColAddress, Kind: KindText},
				{Name: "selection", Kind: KindText},
				{Name: "credential", Kind: KindText},
				{Name: "profile", Kind: KindText},
				{Name: "profile_path", Kind: KindText},
				{Name: "options", Kind: KindText},
				{Name: portColState, Kind: KindText},
				{Name: portColReason, Kind: KindText},
				{Name: portColCreatedBy, Kind: KindText},
				{Name: portColCreatedAtMs, Kind: KindTime},
				{Name: "plan", Kind: KindText},
				{Name: "ended_at_ms", Kind: KindTime},
			},
		},
		{
			Table:   "imports_purged",
			OrderBy: []string{portColNamespaceID, "id"},
			Columns: []Column{
				{Name: portColNamespaceID, Kind: KindText},
				{Name: "id", Kind: KindText},
				{Name: "purged_at_ms", Kind: KindTime},
			},
		},
	}
}

// portableImportUnits describes import_units and import_ledger.
func portableImportUnits() []PortableTable {
	return []PortableTable{
		{
			Table:   "import_units",
			OrderBy: []string{portColNamespaceID, portColImportID, "id"},
			Columns: []Column{
				{Name: portColNamespaceID, Kind: KindText},
				{Name: portColImportID, Kind: KindText},
				{Name: "id", Kind: KindText},
				{Name: "position", Kind: KindInteger},
				{Name: "source_tool", Kind: KindText},
				{Name: "source_name", Kind: KindText},
				{Name: "target_name", Kind: KindText},
				{Name: "target_tool", Kind: KindText},
				{Name: "target_unit_id", Kind: KindText},
				{Name: "state", Kind: KindText},
				{Name: "error_code", Kind: KindText},
				{Name: "versions", Kind: KindInteger},
				{Name: "committed", Kind: KindInteger},
				{Name: "bytes", Kind: KindInteger},
				{Name: "encryption", Kind: KindText},
				{Name: "findings", Kind: KindText},
			},
		},
		{
			Table:   "import_ledger",
			OrderBy: []string{portColNamespaceID, portColImportID, "import_unit_id", portColVersion},
			Columns: []Column{
				{Name: portColNamespaceID, Kind: KindText},
				{Name: portColImportID, Kind: KindText},
				{Name: "import_unit_id", Kind: KindText},
				{Name: portColVersion, Kind: KindText},
				{Name: "source_sha256", Kind: KindText},
				{Name: "stored_sha256", Kind: KindText},
				{Name: "item_tool", Kind: KindText},
				{Name: "item_unit_id", Kind: KindText},
				{Name: "item_kind", Kind: KindText},
				{Name: "item_id", Kind: KindText},
				{Name: "committed_at_ms", Kind: KindTime},
			},
		},
	}
}

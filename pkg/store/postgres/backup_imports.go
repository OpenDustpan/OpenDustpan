package postgres

// pgColImportID is the column that names an import in the import tables.
const pgColImportID = "import_id"

// pgCopyImportTables holds the COPY columns of the tables of the import tool,
// in the order of backup.Tables.
func pgCopyImportTables() []pgCopyTable {
	return []pgCopyTable{
		{
			table: "imports",
			columns: []string{
				pgColNamespaceID,
				"id",
				pgColKind,
				"address",
				"selection",
				"credential",
				"profile",
				"profile_path",
				"options",
				pgColState,
				"reason",
				"created_by",
				"created_at_ms",
				"plan",
				"ended_at_ms",
			},
			orderBy: []string{pgColNamespaceID, "id"},
		},
		{
			table:   "imports_purged",
			columns: []string{pgColNamespaceID, "id", "purged_at_ms"},
			orderBy: []string{pgColNamespaceID, "id"},
		},
		{
			table: "import_units",
			columns: []string{
				pgColNamespaceID,
				pgColImportID,
				"id",
				"position",
				"source_tool",
				"source_name",
				"target_name",
				"target_tool",
				"target_unit_id",
				pgColState,
				"error_code",
				"versions",
				"committed",
				"bytes",
				"encryption",
				"findings",
			},
			orderBy: []string{pgColNamespaceID, pgColImportID, "id"},
		},
		{
			table: "import_ledger",
			columns: []string{
				pgColNamespaceID,
				pgColImportID,
				"import_unit_id",
				pgColVersion,
				"source_sha256",
				"stored_sha256",
				"item_tool",
				"item_unit_id",
				"item_kind",
				"item_id",
				"committed_at_ms",
			},
			orderBy: []string{pgColNamespaceID, pgColImportID, "import_unit_id", pgColVersion},
		},
	}
}

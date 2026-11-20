package postgres

// pgColImportID is the column that names an import in the import tables.
const pgColImportID = "import_id"

// pgCopyImportTables holds the COPY columns of the tables of the import tool,
// in the order of backup.Tables.
func pgCopyImportTables() []pgCopyTable {
	return append([]pgCopyTable{
		{
			table: "imports",
			columns: []string{
				pgColNamespaceID,
				"id",
				pgColKind,
				pgColAddress,
				"selection",
				"credential",
				"profile",
				"profile_path",
				"options",
				pgColState,
				pgColReason,
				pgColCreatedBy,
				"created_at_ms",
				"plan",
				pgColEndedAtMs,
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
				pgColPosition,
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
	}, pgCopyUploadCommits())
}

// pgCopyUploadCommits holds the COPY columns of import_upload_commits.
func pgCopyUploadCommits() pgCopyTable {
	return pgCopyTable{
		table: "import_upload_commits",
		columns: []string{
			pgColNamespaceID,
			pgColImportID,
			"unit_dir",
			pgColSequence,
			"facts",
			"committed_at_ms",
		},
		orderBy: []string{pgColNamespaceID, pgColImportID, "unit_dir", pgColSequence},
	}
}

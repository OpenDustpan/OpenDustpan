package sqlite

// backupColImportID is the column that names an import in the import tables.
const backupColImportID = "import_id"

// sqliteImportBackupTables holds the backup columns of the tables of the
// import tool, in the order of backup.Tables.
func sqliteImportBackupTables() []sqliteBackupTable {
	return append([]sqliteBackupTable{
		{
			table: "imports",
			columns: []string{
				backupColNamespaceID,
				"id",
				backupColKind,
				backupColAddress,
				"selection",
				"credential",
				"profile",
				"profile_path",
				"options",
				backupColState,
				backupColReason,
				backupColCreatedBy,
				backupColCreatedAtMs,
				"plan",
				backupColEndedAtMs,
			},
		},
		{
			table:   "imports_purged",
			columns: []string{backupColNamespaceID, "id", "purged_at_ms"},
		},
		{
			table: "import_units",
			columns: []string{
				backupColNamespaceID,
				backupColImportID,
				"id",
				backupColPosition,
				"source_tool",
				"source_name",
				"target_name",
				"target_tool",
				"target_unit_id",
				backupColState,
				"error_code",
				"versions",
				"committed",
				"bytes",
				"encryption",
				"findings",
			},
		},
		{
			table: "import_ledger",
			columns: []string{
				backupColNamespaceID,
				backupColImportID,
				"import_unit_id",
				backupColVersion,
				"source_sha256",
				"stored_sha256",
				"item_tool",
				"item_unit_id",
				"item_kind",
				"item_id",
				"committed_at_ms",
			},
		},
	}, sqliteUploadCommits())
}

// sqliteUploadCommits holds the backup columns of
// import_upload_commits.
func sqliteUploadCommits() sqliteBackupTable {
	return sqliteBackupTable{
		table: "import_upload_commits",
		columns: []string{
			backupColNamespaceID,
			backupColImportID,
			"unit_dir",
			"sequence",
			"facts",
			"committed_at_ms",
		},
	}
}

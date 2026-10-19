package sqlite

// backupColDocumentSha256 is the column that holds the hash of a document.
const backupColDocumentSha256 = "document_sha256"

// sqliteProfileBackupTables holds the backup columns of the profile tables,
// in the order of backup.Tables.
func sqliteProfileBackupTables() []sqliteBackupTable {
	return []sqliteBackupTable{
		{
			table: "profiles",
			columns: []string{
				backupColNamespaceID, "id", backupColName, "latest", backupColCreatedBy, backupColCreatedAtMs,
				backupColUpdatedAtMs, "deleted_at_ms",
			},
		},
		{
			table: "profile_revisions",
			columns: []string{
				backupColNamespaceID, "profile_id", "number", "document", backupColDocumentSha256, "author",
				backupColCreatedAtMs, "reason",
			},
		},
	}
}

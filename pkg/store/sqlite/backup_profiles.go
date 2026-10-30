package sqlite

// backupColDocumentSha256 is the column that holds the hash of a document.
const backupColDocumentSha256 = "document_sha256"

// backupColBody is the column that holds a body.
const backupColBody = "body"

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
				backupColCreatedAtMs, backupColReason,
			},
		},
		{
			table: "issuer_keys",
			columns: []string{
				backupColNamespaceID, "id", "state", "public_key", "sealed", backupColCreatedAtMs, "activated_at_ms",
				"retired_at_ms", "deleted_at_ms",
			},
		},
		{
			table: "issuer_settings",
			columns: []string{
				backupColNamespaceID, "issuer_url", "subject_template", "allow_block_subjects", "include_actor_name",
				"version", backupColUpdatedAtMs, "updated_by", "copy_check",
			},
		},
	}
}

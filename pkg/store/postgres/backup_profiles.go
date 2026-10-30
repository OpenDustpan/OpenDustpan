package postgres

// pgColDocumentSha256 is the column that holds the hash of a document.
const pgColDocumentSha256 = "document_sha256"

// pgColBody is the column that holds a body.
const pgColBody = "body"

// pgCopyProfileTables holds the COPY columns of the profile tables and the issuer tables, in the
// order of backup.Tables.
func pgCopyProfileTables() []pgCopyTable {
	return []pgCopyTable{
		{
			table: "profiles",
			columns: []string{
				pgColNamespaceID, "id", pgColName, "latest", pgColCreatedBy, pgColCreatedAtMs, pgColUpdatedAtMs,
				"deleted_at_ms",
			},
			orderBy: []string{pgColNamespaceID, "id"},
		},
		{
			table: "profile_revisions",
			columns: []string{
				pgColNamespaceID, "profile_id", "number", "document", pgColDocumentSha256, "author", pgColCreatedAtMs,
				pgColReason,
			},
			orderBy: []string{pgColNamespaceID, "profile_id", "number"},
		},
		{
			table: "issuer_keys",
			columns: []string{
				pgColNamespaceID, "id", "state", "public_key", "sealed", pgColCreatedAtMs, "activated_at_ms",
				"retired_at_ms", "deleted_at_ms",
			},
			orderBy: []string{pgColNamespaceID, "id"},
		},
		{
			table: "issuer_settings",
			columns: []string{
				pgColNamespaceID, "issuer_url", "subject_template", "allow_block_subjects", "include_actor_name",
				"version", pgColUpdatedAtMs, "updated_by", "copy_check",
			},
			orderBy: []string{pgColNamespaceID},
		},
	}
}

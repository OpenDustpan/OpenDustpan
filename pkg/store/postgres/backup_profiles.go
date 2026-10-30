package postgres

// pgColDocumentSha256 is the column that holds the hash of a document.
const pgColDocumentSha256 = "document_sha256"

// pgColBody is the column that holds a body.
const pgColBody = "body"

// pgCopyProfileTables holds the COPY columns of the profile tables, in the
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
				pgColNamespaceID,
				"profile_id",
				pgColNumber,
				"document",
				pgColDocumentSha256,
				"author",
				pgColCreatedAtMs,
				pgColReason,
			},
			orderBy: []string{pgColNamespaceID, "profile_id", pgColNumber},
		},
	}
}

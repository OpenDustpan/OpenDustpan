package postgres

// Column names that the blame tables share with other tables.
const (
	pgColWorkspaceID    = "workspace_id"
	pgColStateVersionID = "state_version_id"
	pgColOrdinal        = "ordinal"
	pgColResourceCount  = "resource_count"
	pgColAddress        = "address"
)

// pgCopyBlameTables holds the COPY columns of the tables of the blame of
// workspaces, in the order of backup.Tables.
func pgCopyBlameTables() []pgCopyTable {
	return []pgCopyTable{
		{
			table:   "workspace_blame_cursors",
			columns: []string{pgColWorkspaceID, pgColOrdinal},
			orderBy: []string{pgColWorkspaceID},
		},
		{
			table: "workspace_blame_items",
			columns: []string{
				pgColWorkspaceID, pgColStateVersionID, pgColOrdinal, "opaque", "lineage_changed", pgColResourceCount,
			},
			orderBy: []string{pgColWorkspaceID, pgColStateVersionID},
		},
		{
			table: "workspace_resource_changes",
			columns: []string{
				pgColWorkspaceID, pgColStateVersionID, pgColAddress, pgColOrdinal, pgColKind, "at_ms", "by_subject",
				"by_token_id", "by_job_id", "changed_paths", "restored", "forced", "lineage_changed",
			},
			orderBy: []string{pgColWorkspaceID, pgColStateVersionID, pgColAddress},
		},
	}
}

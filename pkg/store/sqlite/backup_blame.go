package sqlite

// Column names that the blame tables share with other tables.
const (
	backupColWorkspaceID    = "workspace_id"
	backupColStateVersionID = "state_version_id"
	backupColOrdinal        = "ordinal"
	backupColResourceCount  = "resource_count"
	backupColAddress        = "address"
)

// sqliteBlameBackupTables holds the backup columns of the tables of the blame
// of workspaces, in the order of backup.Tables.
func sqliteBlameBackupTables() []sqliteBackupTable {
	return []sqliteBackupTable{
		{
			table:   "workspace_blame_cursors",
			columns: []string{backupColWorkspaceID, backupColOrdinal},
		},
		{
			table: "workspace_blame_items",
			columns: []string{
				backupColWorkspaceID,
				backupColStateVersionID,
				backupColOrdinal,
				"opaque",
				"lineage_changed",
				backupColResourceCount,
			},
		},
		{
			table: "workspace_resource_changes",
			columns: []string{
				backupColWorkspaceID,
				backupColStateVersionID,
				backupColAddress,
				backupColOrdinal,
				backupColKind,
				"at_ms",
				"by_subject",
				"by_token_id",
				"by_job_id",
				"changed_paths",
				"restored",
				"forced",
				"lineage_changed",
			},
		},
	}
}

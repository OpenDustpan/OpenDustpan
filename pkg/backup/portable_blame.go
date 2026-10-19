package backup

// Column names that the blame tables share with other tables.
const (
	portColWorkspaceID    = "workspace_id"
	portColStateVersionID = "state_version_id"
	portColOrdinal        = "ordinal"
	portColResourceCount  = "resource_count"
	portColAddress        = "address"
)

// portableBlameTables is the portable description of the tables of the blame
// of workspaces (workspace_blame_cursors, workspace_blame_items and
// workspace_resource_changes), in the order of Tables.
func portableBlameTables() []PortableTable {
	return []PortableTable{
		{
			Table:   "workspace_blame_cursors",
			OrderBy: []string{portColWorkspaceID},
			Columns: []Column{
				{Name: portColWorkspaceID, Kind: KindText},
				{Name: portColOrdinal, Kind: KindInteger},
			},
		},
		{
			Table:   "workspace_blame_items",
			OrderBy: []string{portColWorkspaceID, portColStateVersionID},
			Columns: []Column{
				{Name: portColWorkspaceID, Kind: KindText},
				{Name: portColStateVersionID, Kind: KindText},
				{Name: portColOrdinal, Kind: KindInteger},
				{Name: "opaque", Kind: KindBool},
				{Name: "lineage_changed", Kind: KindBool},
				{Name: portColResourceCount, Kind: KindInteger},
			},
		},
		{
			Table:   "workspace_resource_changes",
			OrderBy: []string{portColWorkspaceID, portColStateVersionID, portColAddress},
			Columns: []Column{
				{Name: portColWorkspaceID, Kind: KindText},
				{Name: portColStateVersionID, Kind: KindText},
				{Name: portColAddress, Kind: KindText},
				{Name: portColOrdinal, Kind: KindInteger},
				{Name: portColKind, Kind: KindText},
				{Name: "at_ms", Kind: KindTime},
				{Name: "by_subject", Kind: KindText},
				{Name: "by_token_id", Kind: KindText},
				{Name: "by_job_id", Kind: KindText},
				{Name: "changed_paths", Kind: KindInteger},
				{Name: "restored", Kind: KindBool},
				{Name: "forced", Kind: KindBool},
				{Name: "lineage_changed", Kind: KindBool},
			},
		},
	}
}

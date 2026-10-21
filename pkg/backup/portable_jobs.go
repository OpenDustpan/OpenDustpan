package backup

// portableJobTables is the portable description of the tables of jobs,
// change freezes and the state records that the job rules read, in the order
// of Tables.
func portableJobTables() []PortableTable {
	return []PortableTable{
		{
			Table:   "jobs",
			OrderBy: []string{portColSeq},
			Columns: []Column{
				{Name: "id", Kind: KindText},
				{Name: portColNamespaceID, Kind: KindText},
				{Name: portColUnitID, Kind: KindText},
				{Name: "seq", Kind: KindInteger},
				{Name: portColState, Kind: KindText},
				{Name: "wait", Kind: KindText},
				{Name: "current_phase", Kind: KindInteger},
				{Name: "current_changes", Kind: KindBool},
				{Name: "current_operation", Kind: KindText},
				{Name: "idempotency_key", Kind: KindText},
				{Name: "version", Kind: KindInteger},
				{Name: portColBody, Kind: KindText},
				{Name: portColCreatedAtMs, Kind: KindTime},
				{Name: portColUpdatedAtMs, Kind: KindTime},
			},
		},
		{
			Table:   "job_freezes",
			OrderBy: []string{"id"},
			Columns: []Column{
				{Name: "id", Kind: KindText},
				{Name: portColNamespaceID, Kind: KindText},
				{Name: portColReason, Kind: KindText},
				{Name: "starts_at_ms", Kind: KindTime},
				{Name: "ends_at_ms", Kind: KindTime},
				{Name: portColCreatedBy, Kind: KindText},
				{Name: "ended_by", Kind: KindText},
			},
		},
		{
			Table:   "unit_state_versions",
			OrderBy: []string{portColUnitID},
			Columns: []Column{
				{Name: portColUnitID, Kind: KindText},
				{Name: portColNamespaceID, Kind: KindText},
				{Name: "version_id", Kind: KindText},
				{Name: portColUpdatedAtMs, Kind: KindTime},
			},
		},
		{
			Table:   "unit_final_versions",
			OrderBy: []string{portColUnitID},
			Columns: []Column{
				{Name: portColUnitID, Kind: KindText},
				{Name: portColNamespaceID, Kind: KindText},
				{Name: "version_id", Kind: KindText},
				{Name: "commit_id", Kind: KindText},
				{Name: portColJobID, Kind: KindText},
				{Name: "at_ms", Kind: KindTime},
			},
		},
	}
}

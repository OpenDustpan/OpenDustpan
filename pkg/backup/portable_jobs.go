package backup

// portableJobTables is the portable description of the tables of jobs and
// change freezes, in the order of Tables.
func portableJobTables() []PortableTable {
	return []PortableTable{
		{
			Table:   "jobs",
			OrderBy: []string{portColSeq},
			Columns: []Column{
				{Name: "id", Kind: KindText},
				{Name: portColNamespaceID, Kind: KindText},
				{Name: portColUnitID, Kind: KindText},
				{Name: portColSeq, Kind: KindInteger},
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
				{Name: "state_changed_at_ms", Kind: KindTime},
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
	}
}

// portableJobArtifactTables is the portable description of the tables of plans, log
// versions and log chunks, in the order of Tables.
func portableJobArtifactTables() []PortableTable {
	return []PortableTable{
		{
			Table:   "job_plans",
			OrderBy: []string{"id"},
			Columns: []Column{
				{Name: "id", Kind: KindText},
				{Name: portColNamespaceID, Kind: KindText},
				{Name: portColJobID, Kind: KindText},
				{Name: portColBody, Kind: KindText},
				{Name: portColCreatedAtMs, Kind: KindTime},
			},
		},
		{
			Table:   "job_logs",
			OrderBy: []string{"id"},
			Columns: []Column{
				{Name: "id", Kind: KindText},
				{Name: portColNamespaceID, Kind: KindText},
				{Name: portColJobID, Kind: KindText},
				{Name: "phase", Kind: KindInteger},
				{Name: "step", Kind: KindText},
				{Name: "token", Kind: KindInteger},
				{Name: portColCreatedAtMs, Kind: KindTime},
			},
		},
		{
			Table:   "job_log_chunks",
			OrderBy: []string{"log_id", portColSeq},
			Columns: []Column{
				{Name: "log_id", Kind: KindText},
				{Name: portColSeq, Kind: KindInteger},
				{Name: portColNamespaceID, Kind: KindText},
				{Name: portColSHA256Text, Kind: KindText},
				{Name: "size", Kind: KindInteger},
				{Name: "confirmed", Kind: KindInteger},
				{Name: "stored_size", Kind: KindInteger},
				{Name: "stored_md5", Kind: KindBytes},
				{Name: portColBlobKey, Kind: KindText},
			},
		},
	}
}

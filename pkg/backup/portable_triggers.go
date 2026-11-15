package backup

// portableTriggerTables is the portable description of the trigger table, in
// the order of Tables. The flag enabled is an integer, 0 or 1, in both
// dialects, so a restore checks the value itself.
func portableTriggerTables() []PortableTable {
	return []PortableTable{
		{
			Table:   "triggers",
			OrderBy: []string{portColNamespaceID, "id"},
			Columns: []Column{
				{Name: portColNamespaceID, Kind: KindText},
				{Name: "id", Kind: KindText},
				{Name: "unit_tool", Kind: KindText},
				{Name: portColUnitID, Kind: KindText},
				{Name: portColKind, Kind: KindText},
				{Name: "preset", Kind: KindText},
				{Name: "cron", Kind: KindText},
				{Name: "source_namespace", Kind: KindText},
				{Name: "source_tool", Kind: KindText},
				{Name: "source_unit_id", Kind: KindText},
				{Name: "enabled", Kind: KindInteger},
				{Name: "last_saver_subject", Kind: KindText},
				{Name: "last_saver_id", Kind: KindText},
				{Name: portColCreatedAtMs, Kind: KindTime},
				{Name: portColUpdatedAtMs, Kind: KindTime},
				{Name: "next_slot_ms", Kind: KindInteger},
			},
		},
	}
}

package backup

// portableBreakGlassReviewTables is the portable description of the
// break-glass review table, in the order of Tables.
func portableBreakGlassReviewTables() []PortableTable {
	return []PortableTable{
		{
			Table:   "break_glass_reviews",
			OrderBy: []string{"id"},
			Columns: []Column{
				{Name: "id", Kind: KindText},
				{Name: "use_kind", Kind: KindText},
				{Name: "use_ref", Kind: KindText},
				{Name: "use_scope", Kind: KindText},
				{Name: "use_object", Kind: KindText},
				{Name: "use_subject", Kind: KindText},
				{Name: "use_people", Kind: KindText},
				{Name: "use_reason", Kind: KindText},
				{Name: "use_at_ms", Kind: KindTime},
				{Name: "use_until_ms", Kind: KindTime},
				{Name: portColState, Kind: KindText},
				{Name: "created_at_ms", Kind: KindTime},
				{Name: "due_at_ms", Kind: KindTime},
				{Name: "calendar", Kind: KindText},
				{Name: "calendar_from_namespace", Kind: KindText},
				{Name: "calendar_from_group", Kind: KindText},
				{Name: "step", Kind: KindInteger},
				{Name: "next_notice_at_ms", Kind: KindTime},
				{Name: "verdict_reviewer", Kind: KindText},
				{Name: "verdict_note", Kind: KindText},
				{Name: "verdict_auth", Kind: KindText},
				{Name: "verdict_auth_at_ms", Kind: KindTime},
				{Name: "verdict_reviewed_at_ms", Kind: KindTime},
				{Name: "verdict_act_as", Kind: KindText},
				{Name: portColVersion, Kind: KindInteger},
			},
		},
	}
}

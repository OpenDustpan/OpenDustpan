package backup

// portableFirstAdministratorTables is the portable description of the marker
// "first administrator made", in the order of Tables. The table has at most one
// row, and the column id is always 1. The table of the open code is not here: a
// backup leaves it out (see EmptiedTables).
func portableFirstAdministratorTables() []PortableTable {
	return []PortableTable{
		{
			Table:   TableFirstAdministrator,
			OrderBy: []string{"id"},
			Columns: []Column{
				{Name: "id", Kind: KindInteger},
				{Name: portColPrincipalID, Kind: KindText},
				{Name: "grant_id", Kind: KindText},
				{Name: "session_id", Kind: KindText},
				{Name: portColCreatedBy, Kind: KindText},
				{Name: "made_at_ms", Kind: KindTime},
			},
		},
	}
}

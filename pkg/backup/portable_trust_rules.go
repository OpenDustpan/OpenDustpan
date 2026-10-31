package backup

// portableTrustRuleTables is the portable description of the trust rule
// table, in the order of Tables.
func portableTrustRuleTables() []PortableTable {
	return []PortableTable{
		{
			Table:   TableTrustRules,
			OrderBy: []string{"id"},
			Columns: []Column{
				{Name: "id", Kind: KindText},
				{Name: "namespace", Kind: KindText},
				{Name: portColName, Kind: KindText},
				{Name: portColIssuer, Kind: KindText},
				{Name: "audience", Kind: KindText},
				{Name: "key_source", Kind: KindText},
				{Name: "static_keys", Kind: KindText},
				{Name: "conditions_json", Kind: KindText},
				{Name: portColPrincipalID, Kind: KindText},
				{Name: portColCreatedBy, Kind: KindText},
				{Name: portColCreatedAtMs, Kind: KindTime},
			},
		},
	}
}

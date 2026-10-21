package backup

// portableSinkTables is the portable description of the notification sink
// tables, in the order of Tables. The secret items are sealed values: the
// backup holds the ciphertext, never a plaintext value.
func portableSinkTables() []PortableTable {
	return []PortableTable{
		{
			Table:   "notification_sinks",
			OrderBy: []string{"id"},
			Columns: []Column{
				{Name: "id", Kind: KindText},
				{Name: portColNamespaceID, Kind: KindText},
				{Name: "kind", Kind: KindText},
				{Name: portColName, Kind: KindText},
				{Name: "platform", Kind: KindText},
				{Name: "target", Kind: KindText},
				{Name: portColCreatedBy, Kind: KindText},
				{Name: portColVersion, Kind: KindInteger},
			},
		},
		{
			Table:   "notification_sink_secrets",
			OrderBy: []string{portColNamespaceID, "ref"},
			Columns: []Column{
				{Name: portColNamespaceID, Kind: KindText},
				{Name: "ref", Kind: KindText},
				{Name: "sink_id", Kind: KindText},
				{Name: "position", Kind: KindInteger},
				{Name: "ciphertext", Kind: KindBytes},
			},
		},
		{
			Table:   "notification_sink_refs",
			OrderBy: []string{portColNamespaceID, "ref"},
			Columns: []Column{
				{Name: portColNamespaceID, Kind: KindText},
				{Name: "ref", Kind: KindText},
			},
		},
	}
}

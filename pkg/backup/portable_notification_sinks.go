package backup

// Column names of the sink tables that other tables share.
const (
	portColRef        = "ref"
	portColCiphertext = "ciphertext"
)

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
			OrderBy: []string{portColNamespaceID, portColRef},
			Columns: []Column{
				{Name: portColNamespaceID, Kind: KindText},
				{Name: portColRef, Kind: KindText},
				{Name: "sink_id", Kind: KindText},
				{Name: portColPosition, Kind: KindInteger},
				{Name: portColCiphertext, Kind: KindBytes},
			},
		},
		{
			Table:   "notification_sink_refs",
			OrderBy: []string{portColNamespaceID, portColRef},
			Columns: []Column{
				{Name: portColNamespaceID, Kind: KindText},
				{Name: portColRef, Kind: KindText},
			},
		},
	}
}

package backup

// Column names of the sink tables that other tables share.
const (
	portColScope      = "scope"
	portColRef        = "ref"
	portColCiphertext = "ciphertext"
)

// portableSinkTables is the portable description of the notification tables,
// in the order of Tables. The secret items are sealed values: the backup holds
// the ciphertext, never a plaintext value. The scope of a row is the name of a
// namespace, or the server scope.
func portableSinkTables() []PortableTable {
	return []PortableTable{
		{
			Table:   "notification_sinks",
			OrderBy: []string{"id"},
			Columns: []Column{
				{Name: "id", Kind: KindText},
				{Name: portColScope, Kind: KindText},
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
			OrderBy: []string{portColScope, portColRef},
			Columns: []Column{
				{Name: portColScope, Kind: KindText},
				{Name: portColRef, Kind: KindText},
				{Name: "sink_id", Kind: KindText},
				{Name: "position", Kind: KindInteger},
				{Name: portColCiphertext, Kind: KindBytes},
			},
		},
		{
			Table:   "notification_sink_refs",
			OrderBy: []string{portColScope, portColRef},
			Columns: []Column{
				{Name: portColScope, Kind: KindText},
				{Name: portColRef, Kind: KindText},
			},
		},
		{
			Table:   "notifications",
			OrderBy: []string{portColScope, "sequence"},
			Columns: []Column{
				{Name: portColScope, Kind: KindText},
				{Name: "sequence", Kind: KindInteger},
				{Name: "id", Kind: KindText},
				{Name: "type", Kind: KindText},
				{Name: "object", Kind: KindText},
				{Name: "object_id", Kind: KindText},
				{Name: "subject", Kind: KindText},
				{Name: "agent", Kind: KindText},
				{Name: "severity", Kind: KindInteger},
				{Name: "summary", Kind: KindText},
				{Name: "links", Kind: KindText},
				{Name: "attributes", Kind: KindText},
				{Name: "recipients", Kind: KindText},
				{Name: "dedupe_key", Kind: KindText},
				{Name: portColAtMs, Kind: KindTime},
			},
		},
		{
			Table:   "notification_sequences",
			OrderBy: []string{portColScope},
			Columns: []Column{
				{Name: portColScope, Kind: KindText},
				{Name: "last_sequence", Kind: KindInteger},
			},
		},
		{
			Table:   "notification_cursors",
			OrderBy: []string{portColName, portColScope},
			Columns: []Column{
				{Name: portColName, Kind: KindText},
				{Name: portColScope, Kind: KindText},
				{Name: "sequence", Kind: KindInteger},
			},
		},
		{
			Table:   "notification_subscriptions",
			OrderBy: []string{"id"},
			Columns: []Column{
				{Name: "id", Kind: KindText},
				{Name: portColScope, Kind: KindText},
				{Name: "sink_id", Kind: KindText},
				{Name: "types", Kind: KindText},
				{Name: "objects", Kind: KindText},
				{Name: "min_severity", Kind: KindInteger},
				{Name: "state", Kind: KindText},
				{Name: "reason", Kind: KindText},
				{Name: portColCreatedBy, Kind: KindText},
				{Name: portColVersion, Kind: KindInteger},
			},
		},
		{
			Table:   "notification_deliveries",
			OrderBy: []string{"id"},
			Columns: []Column{
				{Name: "id", Kind: KindText},
				{Name: portColScope, Kind: KindText},
				{Name: "notification_id", Kind: KindText},
				{Name: "subscription_id", Kind: KindText},
				{Name: "sink_id", Kind: KindText},
				{Name: "contact", Kind: KindText},
				{Name: "principal", Kind: KindText},
				{Name: "state", Kind: KindText},
				{Name: "attempts", Kind: KindInteger},
				{Name: "next_at_ms", Kind: KindInteger},
				{Name: "last_error", Kind: KindText},
				{Name: "delivered_at_ms", Kind: KindInteger},
				{Name: "owner", Kind: KindText},
				{Name: "lease_until_ms", Kind: KindInteger},
				{Name: portColVersion, Kind: KindInteger},
			},
		},
	}
}

package backup

// Column names of the tables of the model connection that other tables share.
const (
	// portColChangedAtMs is the column of the time of the last change of a
	// secret item or of the current connection document.
	portColChangedAtMs = "changed_at_ms"
	// portColLastUsedAtMs is the column of the time of the last use of a
	// secret item or of an access token.
	portColLastUsedAtMs = "last_used_at_ms"
	// portColHost is the column of a host name.
	portColHost = "host"
)

// portableAgentConnectionTables is the portable description of the tables of
// the model connection, in the order of Tables: the secret items, the current
// connection document, the reference rows of the secret items and the egress
// allowlist. A secret item is a sealed value: the backup holds the ciphertext,
// never a plain value. The secret items come before the reference rows, which
// name them. The pending documents are not here: see EmptiedTables.
func portableAgentConnectionTables() []PortableTable {
	return []PortableTable{
		{
			Table:   "agent_secret_items",
			OrderBy: []string{portColRef},
			Columns: []Column{
				{Name: portColRef, Kind: KindText},
				{Name: portColName, Kind: KindText},
				{Name: portColCiphertext, Kind: KindBytes},
				{Name: portColCreatedAtMs, Kind: KindTime},
				{Name: portColChangedAtMs, Kind: KindTime},
				// Zero is an item that no reader opened yet, so the value is
				// an integer, not a time.
				{Name: portColLastUsedAtMs, Kind: KindInteger},
			},
		},
		{
			Table:   "agent_connection_current",
			OrderBy: []string{portColRef},
			Columns: []Column{
				{Name: portColRef, Kind: KindText},
				{Name: portColDocument, Kind: KindBytes},
				{Name: portColVersion, Kind: KindInteger},
				{Name: portColCreatedAtMs, Kind: KindTime},
				{Name: portColChangedAtMs, Kind: KindTime},
			},
		},
		{
			Table:   "agent_secret_refs",
			OrderBy: []string{portColRef, "secret"},
			Columns: []Column{
				{Name: portColRef, Kind: KindText},
				{Name: "secret", Kind: KindText},
			},
		},
		{
			Table:   "agent_egress_allowlist",
			OrderBy: []string{"id"},
			Columns: []Column{
				{Name: "id", Kind: KindInteger},
				{Name: portColUpdatedAtMs, Kind: KindTime},
			},
		},
		{
			Table:   "agent_egress_allowlist_hosts",
			OrderBy: []string{"id"},
			Columns: []Column{
				{Name: "id", Kind: KindInteger},
				{Name: portColHost, Kind: KindText},
			},
		},
		{
			Table:   "agent_egress_allowlist_rules",
			OrderBy: []string{"id"},
			Columns: []Column{
				{Name: "id", Kind: KindInteger},
				{Name: "cidr", Kind: KindText},
				{Name: "port", Kind: KindInteger},
				{Name: "http", Kind: KindInteger},
			},
		},
	}
}

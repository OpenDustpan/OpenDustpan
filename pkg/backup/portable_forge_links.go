package backup

// portColLinkedAtMs is the column that holds the time of a link.
const portColLinkedAtMs = "linked_at_ms"

// portableForgeLinkTables is the portable description of the tables of the
// links from Git host accounts to principals and of the requests that make
// them, in the order of Tables. The columns proved_at_ms and used_at_ms are
// integers: the value 0 means that the request has no proof or no use.
func portableForgeLinkTables() []PortableTable {
	return []PortableTable{
		{
			Table:   TableForgeAccountLinks,
			OrderBy: []string{portColKind, "host", "account_id"},
			Columns: []Column{
				{Name: portColKind, Kind: KindText},
				{Name: "host", Kind: KindText},
				{Name: "account_id", Kind: KindText},
				{Name: "login", Kind: KindText},
				{Name: portColPrincipalID, Kind: KindText},
				{Name: portColLinkedAtMs, Kind: KindTime},
			},
		},
		{
			Table:   TableForgeLinkRequests,
			OrderBy: []string{"token_sha256"},
			Columns: []Column{
				{Name: "token_sha256", Kind: KindText},
				{Name: portColPrincipalID, Kind: KindText},
				{Name: portColCreatedAtMs, Kind: KindTime},
				{Name: portColExpiresAtMs, Kind: KindTime},
				{Name: "proved_kind", Kind: KindText},
				{Name: "proved_host", Kind: KindText},
				{Name: "proved_account_id", Kind: KindText},
				{Name: "proved_login", Kind: KindText},
				{Name: "proved_delivery_id", Kind: KindText},
				{Name: "proved_at_ms", Kind: KindInteger},
				{Name: "used_at_ms", Kind: KindInteger},
			},
		},
	}
}

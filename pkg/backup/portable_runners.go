package backup

// Column names of the runner tables that more than one table holds.
const (
	portColSHA256      = "sha256"
	portColRevokedAtMs = "revoked_at_ms"
)

// portableProxySettingsTable describes the table of the proxy settings of a
// pool. The credential column holds sealed bytes that the backup keeps as they
// are.
func portableProxySettingsTable() PortableTable {
	return PortableTable{
		Table:   "runner_pool_proxy_settings",
		OrderBy: []string{portColPoolID},
		Columns: []Column{
			{Name: portColPoolID, Kind: KindText},
			{Name: "allowed_ranges", Kind: KindText},
			{Name: "upstream_url", Kind: KindText},
			{Name: "credential", Kind: KindBytes},
			{Name: portColVersion, Kind: KindInteger},
			{Name: portColUpdatedBy, Kind: KindText},
			{Name: portColUpdatedAtMs, Kind: KindTime},
		},
	}
}

// portableRunnerTables is the portable description of the tables of runner
// pools, join tokens and runners, in the order of Tables. The tables of
// handoff tokens, job sessions, claim times and nonces are control data and
// are not in a backup.
func portableRunnerTables() []PortableTable {
	return []PortableTable{
		{
			Table:   "runner_pools",
			OrderBy: []string{"id"},
			Columns: []Column{
				{Name: "id", Kind: KindText},
				{Name: portColNamespaceID, Kind: KindText},
				{Name: portColName, Kind: KindText},
				{Name: "purpose", Kind: KindText},
				{Name: "lease_ms", Kind: KindInteger},
				{Name: "concurrency", Kind: KindInteger},
				{Name: "isolation", Kind: KindText},
				{Name: "git_mode", Kind: KindText},
				{Name: "git_helper", Kind: KindText},
				{Name: portColCreatedAtMs, Kind: KindTime},
				{Name: portColUpdatedAtMs, Kind: KindTime},
			},
		},
		portableProxySettingsTable(),
		{
			Table:   "runner_join_tokens",
			OrderBy: []string{"id"},
			Columns: []Column{
				{Name: "id", Kind: KindText},
				{Name: portColNamespaceID, Kind: KindText},
				{Name: portColPoolID, Kind: KindText},
				{Name: portColSHA256, Kind: KindText},
				{Name: portColCreatedBy, Kind: KindText},
				{Name: portColCreatedAtMs, Kind: KindTime},
				{Name: "expires_at_ms", Kind: KindTime},
				{Name: "used_at_ms", Kind: KindTime},
				{Name: "used_by", Kind: KindText},
				{Name: portColRevokedAtMs, Kind: KindTime},
			},
		},
		{
			Table:   "runners",
			OrderBy: []string{"id"},
			Columns: []Column{
				{Name: "id", Kind: KindText},
				{Name: portColNamespaceID, Kind: KindText},
				{Name: portColPoolID, Kind: KindText},
				{Name: portColName, Kind: KindText},
				{Name: portColKind, Kind: KindText},
				{Name: "isolation", Kind: KindText},
				{Name: "labels", Kind: KindText},
				{Name: "release", Kind: KindText},
				{Name: "key_public", Kind: KindBytes},
				{Name: "key_created_at_ms", Kind: KindTime},
				{Name: "previous_key_public", Kind: KindBytes},
				{Name: "previous_key_created_at_ms", Kind: KindTime},
				{Name: "rotated_at_ms", Kind: KindTime},
				{Name: "joined_with", Kind: KindText},
				{Name: "draining", Kind: KindBool},
				{Name: "retired_at_ms", Kind: KindTime},
				{Name: "last_seen_at_ms", Kind: KindTime},
				{Name: "joined_at_ms", Kind: KindTime},
			},
		},
	}
}

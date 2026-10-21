package backup

// portColInstallationID is the column that holds the ID of a GitHub App
// installation.
const portColInstallationID = "installation_id"

// portableGitHostTables is the portable description of the Git host tables:
// the registration of the installation-level GitHub App, the map from an
// installation to a namespace, and the connections of the namespaces. It
// follows the order of Tables.
func portableGitHostTables() []PortableTable {
	return []PortableTable{
		{
			Table:   TableGitHostAppRegistrations,
			OrderBy: []string{portColKind},
			Columns: []Column{
				{Name: portColKind, Kind: KindText},
				{Name: "app_id", Kind: KindText},
				{Name: "base_url", Kind: KindText},
				{Name: "hook_id", Kind: KindText},
				{Name: "sealed_key", Kind: KindBytes},
				{Name: "key_set_id", Kind: KindText},
				{Name: "key_created_at_ms", Kind: KindTime},
				{Name: "sealed_event_secret", Kind: KindBytes},
				{Name: "event_secret_set_id", Kind: KindText},
				{Name: "event_secret_created_at_ms", Kind: KindTime},
				{Name: "address_digest", Kind: KindText},
				{Name: portColVersion, Kind: KindInteger},
				{Name: portColCreatedAtMs, Kind: KindTime},
				{Name: portColUpdatedAtMs, Kind: KindTime},
			},
		},
		{
			Table:   "git_host_installations",
			OrderBy: []string{portColInstallationID},
			Columns: []Column{
				{Name: portColInstallationID, Kind: KindText},
				{Name: portColNamespaceID, Kind: KindText},
				{Name: portColVersion, Kind: KindInteger},
				{Name: portColCreatedAtMs, Kind: KindTime},
				{Name: portColUpdatedAtMs, Kind: KindTime},
			},
		},
		{
			Table:   "git_host_connections",
			OrderBy: []string{portColNamespaceID, "id"},
			Columns: []Column{
				{Name: portColNamespaceID, Kind: KindText},
				{Name: "id", Kind: KindText},
				{Name: portColKind, Kind: KindText},
				{Name: "auth", Kind: KindText},
				{Name: "app_scope", Kind: KindText},
				{Name: portColInstallationID, Kind: KindText},
				{Name: "push", Kind: KindBool},
				{Name: "is_default", Kind: KindBool},
				{Name: portColVersion, Kind: KindInteger},
				{Name: portColCreatedAtMs, Kind: KindTime},
				{Name: portColUpdatedAtMs, Kind: KindTime},
			},
		},
	}
}

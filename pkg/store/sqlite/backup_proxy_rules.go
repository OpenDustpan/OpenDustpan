package sqlite

// sqliteProxyRuleBackupTables holds the backup columns of the proxy rule set
// table, in the order of backup.Tables.
func sqliteProxyRuleBackupTables() []sqliteBackupTable {
	return []sqliteBackupTable{
		{
			table: "proxy_rule_sets",
			columns: []string{
				backupColNamespaceID, "scope_kind", backupColUnitID, "document", "digest", backupColVersion,
				backupColUpdatedAtMs,
			},
		},
	}
}

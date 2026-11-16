package sqlite

// sqliteProxyColDocument is the column of the document of a proxy rule set.
const sqliteProxyColDocument = "document"

// sqliteProxyRuleBackupTables holds the backup columns of the proxy rule set
// table, in the order of backup.Tables.
func sqliteProxyRuleBackupTables() []sqliteBackupTable {
	return []sqliteBackupTable{
		{
			table: "proxy_rule_sets",
			columns: []string{
				backupColNamespaceID, "scope_kind", backupColUnitID, sqliteProxyColDocument, "digest", backupColVersion,
				backupColUpdatedAtMs,
			},
		},
	}
}

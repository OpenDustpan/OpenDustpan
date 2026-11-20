package postgres

// pgColProxyDocument is the column of the document of a proxy rule set.
const pgColProxyDocument = "document"

// pgCopyProxyRuleTables holds the COPY columns of the proxy rule set table, in
// the order of backup.Tables.
func pgCopyProxyRuleTables() []pgCopyTable {
	return []pgCopyTable{
		{
			table: "proxy_rule_sets",
			columns: []string{
				pgColNamespaceID, "scope_kind", pgColUnitID, pgColProxyDocument, pgColDigest, pgColVersion,
				pgColUpdatedAtMs,
			},
			orderBy: []string{pgColNamespaceID, "scope_kind", pgColUnitID},
		},
	}
}

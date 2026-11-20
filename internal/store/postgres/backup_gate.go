package postgres

// Column names that the gate tables share with other tables of the backup.
const (
	pgColAtMs     = "at_ms"
	pgColOwner    = "owner"
	pgColScopeKey = "scope_key"
)

// pgCopyGateTables holds the COPY columns of the gate configuration and
// record tables, in foreign-key order.
func pgCopyGateTables() []pgCopyTable {
	return []pgCopyTable{
		{
			table: "gate_objects",
			columns: []string{
				pgColNamespaceID,
				pgColScopeKey,
				pgColKind,
				"id",
				"level",
				pgColUnitID,
				pgColVersion,
				pgColBody,
				pgColUpdatedAtMs,
			},
			orderBy: []string{pgColNamespaceID, pgColScopeKey, pgColKind, "id"},
		},
		{
			table:   "gate_references",
			columns: []string{"ref_kind", "ref_id", pgColNamespaceID, pgColScopeKey, "owner_kind", "owner_id"},
			orderBy: []string{"ref_kind", "ref_id", pgColNamespaceID, pgColScopeKey, "owner_kind", "owner_id"},
		},
		{
			table: "gate_records",
			columns: []string{
				"id", pgColNamespaceID, "point", "change_kind", "change_id", pgColAtMs, "last_at_ms", "count",
				"refusal_key", pgColBody,
			},
			orderBy: []string{"id"},
		},
	}
}

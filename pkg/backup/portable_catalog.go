package backup

// portColNumber is the column that holds the number of a version.
const portColNumber = "number"

// portColExprVersion is the column that holds the Expr version of a blueprint
// version.
const portColExprVersion = "expr_version"

// portableCatalogTables is the portable description of the tables of catalog
// blueprints, in the order of Tables: the blueprints with their drafts, then
// the published versions. The flag retired is an integer, 0 or 1, in both
// dialects, so a restore checks the value itself.
func portableCatalogTables() []PortableTable {
	return []PortableTable{
		{
			Table:   "catalog_blueprints",
			OrderBy: []string{portColNamespaceID, portColName},
			Columns: []Column{
				{Name: portColNamespaceID, Kind: KindText},
				{Name: portColName, Kind: KindText},
				{Name: "draft_version", Kind: KindInteger},
				{Name: "draft_json", Kind: KindText},
				{Name: "draft_updated_at_ms", Kind: KindTime},
			},
		},
		{
			Table:   "catalog_blueprint_versions",
			OrderBy: []string{portColNamespaceID, portColName, portColNumber},
			Columns: []Column{
				{Name: portColNamespaceID, Kind: KindText},
				{Name: portColName, Kind: KindText},
				{Name: portColNumber, Kind: KindInteger},
				{Name: "draft_version", Kind: KindInteger},
				{Name: "document_json", Kind: KindText},
				{Name: portColExprVersion, Kind: KindText},
				{Name: "published_by", Kind: KindText},
				{Name: "published_at_ms", Kind: KindTime},
				{Name: "retired", Kind: KindInteger},
			},
		},
	}
}

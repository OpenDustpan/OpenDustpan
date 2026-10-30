package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"opendustpan.org/opendustpan/internal/catalog"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/internal/catalogstore"
)

// pgColNumber is the column that holds the number of a version.
const pgColNumber = "number"

// pgCopyCatalogTables holds the COPY columns of the tables of catalog
// blueprints, in the order of backup.Tables.
func pgCopyCatalogTables() []pgCopyTable {
	return []pgCopyTable{
		{
			table:   "catalog_blueprints",
			columns: []string{pgColNamespaceID, pgColName, "draft_version", "draft_json", "draft_updated_at_ms"},
			orderBy: []string{pgColNamespaceID, pgColName},
		},
		{
			table: "catalog_blueprint_versions",
			columns: []string{
				pgColNamespaceID, pgColName, pgColNumber, "document_json", "expr_version", "published_by",
				"published_at_ms", "retired",
			},
			orderBy: []string{pgColNamespaceID, pgColName, pgColNumber},
		},
	}
}

// checkStagedCatalogBlueprints checks each draft and each published version of
// a staged backup with the rules of a stored blueprint. A row that fails makes
// the backup corrupt, so a restore never brings in a blueprint that the server
// would not serve. It returns backup.ErrCorrupt.
//
// Defense in depth: the store checks a row again when it reads it. This check
// stays so that a bad row fails the restore and does not wait for a reader.
func checkStagedCatalogBlueprints(ctx context.Context, pool *pgxpool.Pool, staging string) error {
	if err := checkStagedCatalogDrafts(ctx, pool, staging); err != nil {
		return err
	}

	return checkStagedCatalogVersions(ctx, pool, staging)
}

func checkStagedCatalogDrafts(ctx context.Context, pool *pgxpool.Pool, staging string) error {
	query := "SELECT n.name, b.name, b.draft_json FROM " + stagingTable(staging, "catalog_blueprints") +
		" AS b LEFT JOIN " + stagingTable(staging, "namespaces") + " AS n ON n.id = b.namespace_id"

	rows, err := pool.Query(ctx, query)
	if err != nil {
		return fmt.Errorf("read staged blueprints: %w", backup.ErrExportRead)
	}

	defer rows.Close()

	for rows.Next() {
		var (
			ns   *string
			name string
			doc  string
		)

		if scanErr := rows.Scan(&ns, &name, &doc); scanErr != nil {
			return fmt.Errorf("read staged blueprints: %w", backup.ErrExportRead)
		}

		// A blueprint of a namespace that the backup does not hold is corrupt.
		// The LEFT JOIN keeps such a row in the result, and the NULL name fails it.
		if ns == nil {
			return fmt.Errorf("staged blueprint %q has no namespace: %w", name, backup.ErrCorrupt)
		}

		ref := catalog.BlueprintRef{Namespace: namespace.Name(*ns), Name: name}
		if _, draftErr := catalogstore.Draft(ref, doc); draftErr != nil {
			return fmt.Errorf("staged blueprint %q is not valid: %w: %w", name, draftErr, backup.ErrCorrupt)
		}
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		return fmt.Errorf("read staged blueprints: %w", backup.ErrExportRead)
	}

	return nil
}

func checkStagedCatalogVersions(ctx context.Context, pool *pgxpool.Pool, staging string) error {
	query := "SELECT n.name, v.name, v.number, v.document_json, v.expr_version, v.published_by, " +
		"v.published_at_ms, v.retired FROM " + stagingTable(staging, "catalog_blueprint_versions") +
		" AS v LEFT JOIN " + stagingTable(staging, "namespaces") + " AS n ON n.id = v.namespace_id"

	rows, err := pool.Query(ctx, query)
	if err != nil {
		return fmt.Errorf("read staged blueprint versions: %w", backup.ErrExportRead)
	}

	defer rows.Close()

	for rows.Next() {
		var (
			ns  *string
			row catalogstore.VersionRow
		)

		scanErr := rows.Scan(&ns, &row.Name, &row.Number, &row.DocumentJSON, &row.ExprVersion, &row.PublishedBy,
			&row.PublishedAtMs, &row.Retired)
		if scanErr != nil {
			return fmt.Errorf("read staged blueprint versions: %w", backup.ErrExportRead)
		}

		if ns == nil {
			return fmt.Errorf("staged blueprint version %q has no namespace: %w", row.Name, backup.ErrCorrupt)
		}

		row.Namespace = *ns

		if _, verErr := catalogstore.Version(row); verErr != nil {
			return fmt.Errorf("staged blueprint version %q is not valid: %w: %w", row.Name, verErr, backup.ErrCorrupt)
		}
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		return fmt.Errorf("read staged blueprint versions: %w", backup.ErrExportRead)
	}

	return nil
}

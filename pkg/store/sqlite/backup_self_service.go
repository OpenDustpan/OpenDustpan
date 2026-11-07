package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"opendustpan.org/opendustpan/internal/selfservice"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/internal/selfservicestore"
)

// sqliteSelfServiceBackupTables holds the backup columns of the tables of self-service
// blueprints, in the order of backup.Tables.
func sqliteSelfServiceBackupTables() []sqliteBackupTable {
	return []sqliteBackupTable{
		{
			table: "self_service_blueprints",
			columns: []string{
				backupColNamespaceID, backupColName, "draft_version", "draft_json", "draft_updated_at_ms",
			},
		},
		{
			table: "self_service_blueprint_versions",
			columns: []string{
				backupColNamespaceID,
				backupColName,
				"number",
				"draft_version",
				"document_json",
				"expr_version",
				"published_by",
				"published_at_ms",
				"retired",
			},
		},
	}
}

// checkStagedSelfServiceBlueprints checks each draft and each published version of
// a staged backup with the rules of a stored blueprint. A row that fails makes
// the backup corrupt, so a restore never brings in a blueprint that the server
// would not serve. It returns backup.ErrCorrupt.
//
// Defense in depth: the store checks a row again when it reads it. This check
// stays so that a bad row fails the restore and does not wait for a reader.
func checkStagedSelfServiceBlueprints(ctx context.Context, conn *sql.DB) error {
	if err := checkStagedSelfServiceDrafts(ctx, conn); err != nil {
		return err
	}

	return checkStagedSelfServiceVersions(ctx, conn)
}

func checkStagedSelfServiceDrafts(ctx context.Context, conn *sql.DB) error {
	const query = `SELECT n.name, b.name, b.draft_json
FROM self_service_blueprints AS b LEFT JOIN namespaces AS n ON n.id = b.namespace_id`

	rows, err := conn.QueryContext(ctx, query)
	if err != nil {
		return fmt.Errorf("read staged blueprints: %w", backup.ErrExportRead)
	}

	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			_ = closeErr
		}
	}()

	for rows.Next() {
		var (
			ns   sql.NullString
			name string
			doc  string
		)

		if scanErr := rows.Scan(&ns, &name, &doc); scanErr != nil {
			return fmt.Errorf("read staged blueprints: %w", backup.ErrExportRead)
		}

		// A blueprint of a namespace that the backup does not hold is corrupt.
		// The LEFT JOIN keeps such a row in the result, and the NULL name fails it.
		if !ns.Valid {
			return fmt.Errorf("staged blueprint %q has no namespace: %w", name, backup.ErrCorrupt)
		}

		ref := selfservice.BlueprintRef{Namespace: namespace.Name(ns.String), Name: name}
		if _, draftErr := selfservicestore.Draft(ref, doc); draftErr != nil {
			return fmt.Errorf("staged blueprint %q is not valid: %w: %w", name, draftErr, backup.ErrCorrupt)
		}
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		return fmt.Errorf("read staged blueprints: %w", backup.ErrExportRead)
	}

	return nil
}

func checkStagedSelfServiceVersions(ctx context.Context, conn *sql.DB) error {
	const query = `SELECT n.name, v.name, v.number, v.draft_version, v.document_json, v.expr_version, v.published_by,
v.published_at_ms, v.retired
FROM self_service_blueprint_versions AS v LEFT JOIN namespaces AS n ON n.id = v.namespace_id`

	rows, err := conn.QueryContext(ctx, query)
	if err != nil {
		return fmt.Errorf("read staged blueprint versions: %w", backup.ErrExportRead)
	}

	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			_ = closeErr
		}
	}()

	for rows.Next() {
		var (
			ns  sql.NullString
			row selfservicestore.VersionRow
		)

		scanErr := rows.Scan(
			&ns,
			&row.Name,
			&row.Number,
			&row.DraftVersion,
			&row.DocumentJSON,
			&row.ExprVersion,
			&row.PublishedBy,
			&row.PublishedAtMs,
			&row.Retired,
		)
		if scanErr != nil {
			return fmt.Errorf("read staged blueprint versions: %w", backup.ErrExportRead)
		}

		if !ns.Valid {
			return fmt.Errorf("staged blueprint version %q has no namespace: %w", row.Name, backup.ErrCorrupt)
		}

		row.Namespace = ns.String

		if _, verErr := selfservicestore.Version(row); verErr != nil {
			return fmt.Errorf("staged blueprint version %q is not valid: %w: %w", row.Name, verErr, backup.ErrCorrupt)
		}
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		return fmt.Errorf("read staged blueprint versions: %w", backup.ErrExportRead)
	}

	return nil
}

package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/store/internal/registrystore"
)

// sqliteRegistryBackupTables holds the backup columns of the registry release
// table, in the order of backup.Tables.
func sqliteRegistryBackupTables() []sqliteBackupTable {
	return []sqliteBackupTable{
		{
			table: "registry_releases",
			columns: []string{
				backupColNamespaceID, "kind", backupColName, "system", "version", "sums_file", "sums_sha256",
				"signature_file", "signature_sha256", "key_fingerprint", "published_by", "archives_json",
				"published_at_ms",
			},
		},
	}
}

// checkStagedRegistryReleases checks each release row of a staged backup with
// the rules of a new release. A row that fails makes the backup corrupt, so a
// restore never brings in a release that the server would not have stored. It
// returns backup.ErrCorrupt.
//
// Defense in depth: the store checks a row again when it reads it. This check
// stays so that a bad row fails the restore and does not wait for a reader.
func checkStagedRegistryReleases(ctx context.Context, conn *sql.DB) error {
	const query = `SELECT n.name, r.kind, r.name, r.system, r.version, r.sums_file, r.sums_sha256, r.signature_file,
r.signature_sha256, r.key_fingerprint, r.published_by, r.archives_json, r.published_at_ms
FROM registry_releases AS r LEFT JOIN namespaces AS n ON n.id = r.namespace_id`

	rows, err := conn.QueryContext(ctx, query)
	if err != nil {
		return fmt.Errorf("read staged registry releases: %w", backup.ErrExportRead)
	}

	defer func() {
		_ = rows.Close() //nolint:errcheck // Read-only check handle.
	}()

	for rows.Next() {
		var (
			row       registrystore.Row
			namespace sql.NullString
		)

		scanErr := rows.Scan(&namespace, &row.Kind, &row.Name, &row.System, &row.Version, &row.SumsFile,
			&row.SumsSHA256, &row.SignatureFile, &row.SignatureSHA256, &row.KeyFingerprint, &row.PublishedBy,
			&row.ArchivesJSON, &row.PublishedAtMs)
		if scanErr != nil {
			return fmt.Errorf("read staged registry releases: %w", backup.ErrExportRead)
		}

		// A release of a namespace that the backup does not hold is corrupt. The
		// LEFT JOIN keeps such a row in the result, and the NULL name fails it.
		if !namespace.Valid {
			return fmt.Errorf("staged registry release %q has no namespace: %w", row.Name, backup.ErrCorrupt)
		}

		row.Namespace = namespace.String

		if _, relErr := registrystore.Release(row); relErr != nil {
			return fmt.Errorf("staged registry release %q is not valid: %w: %w", row.Name, relErr, backup.ErrCorrupt)
		}
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		return fmt.Errorf("read staged registry releases: %w", backup.ErrExportRead)
	}

	return nil
}

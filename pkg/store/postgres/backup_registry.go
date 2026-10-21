package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/store/internal/registrystore"
)

// Column names of the registry tables that the backup code of this file names.
const (
	pgColFingerprint = "fingerprint"
	pgColAddedAtMs   = "added_at_ms"
)

// pgCopyRegistryTables holds the COPY columns of the registry release table,
// in the order of backup.Tables.
func pgCopyRegistryTables() []pgCopyTable {
	return []pgCopyTable{
		{
			table: "registry_releases",
			columns: []string{
				pgColNamespaceID, pgColKind, pgColName, "system", pgColVersion, "sums_file", "sums_sha256",
				"signature_file", "signature_sha256", "key_fingerprint", "protocols", "published_by", "archives_json",
				"published_at_ms",
			},
			orderBy: []string{pgColNamespaceID, pgColKind, pgColName, "system", pgColVersion},
		},
		{
			table:   "registry_keys",
			columns: []string{pgColNamespaceID, pgColFingerprint, "armored_key", "added_by", pgColAddedAtMs},
			orderBy: []string{pgColNamespaceID, pgColFingerprint},
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
func checkStagedRegistryReleases(ctx context.Context, pool *pgxpool.Pool, staging string) error {
	query := "SELECT n.name, r.kind, r.name, r.system, r.version, r.sums_file, r.sums_sha256, r.signature_file, " +
		"r.signature_sha256, r.key_fingerprint, r.protocols, r.published_by, r.archives_json, r.published_at_ms FROM " +
		stagingTable(staging, "registry_releases") + " AS r LEFT JOIN " + stagingTable(staging, "namespaces") +
		" AS n ON n.id = r.namespace_id"

	rows, err := pool.Query(ctx, query)
	if err != nil {
		return fmt.Errorf("read staged registry releases: %w", backup.ErrExportRead)
	}

	defer rows.Close()

	for rows.Next() {
		var (
			row       registrystore.Row
			namespace *string
		)

		scanErr := rows.Scan(
			&namespace,
			&row.Kind,
			&row.Name,
			&row.System,
			&row.Version,
			&row.SumsFile,
			&row.SumsSHA256,
			&row.SignatureFile,
			&row.SignatureSHA256,
			&row.KeyFingerprint,
			&row.Protocols,
			&row.PublishedBy,
			&row.ArchivesJSON,
			&row.PublishedAtMs,
		)
		if scanErr != nil {
			return fmt.Errorf("read staged registry releases: %w", backup.ErrExportRead)
		}

		// A release of a namespace that the backup does not hold is corrupt. The
		// LEFT JOIN keeps such a row in the result, and the NULL name fails it.
		if namespace == nil {
			return fmt.Errorf("staged registry release %q has no namespace: %w", row.Name, backup.ErrCorrupt)
		}

		row.Namespace = *namespace

		if _, relErr := registrystore.Release(row); relErr != nil {
			return fmt.Errorf("staged registry release %q is not valid: %w: %w", row.Name, relErr, backup.ErrCorrupt)
		}
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		return fmt.Errorf("read staged registry releases: %w", backup.ErrExportRead)
	}

	return nil
}

// checkStagedRegistryKeys checks each key row of a staged backup with the rules
// of a new key. A row that fails makes the backup corrupt. It returns
// backup.ErrCorrupt.
//
// Defense in depth: the store checks a row again when it reads it. This check
// stays so that a bad row fails the restore and does not wait for a reader.
func checkStagedRegistryKeys(ctx context.Context, pool *pgxpool.Pool, staging string) error {
	query := "SELECT n.name, k.fingerprint, k.armored_key, k.added_by, k.added_at_ms FROM " +
		stagingTable(staging, "registry_keys") + " AS k LEFT JOIN " + stagingTable(staging, "namespaces") +
		" AS n ON n.id = k.namespace_id"

	rows, err := pool.Query(ctx, query)
	if err != nil {
		return fmt.Errorf("read staged registry keys: %w", backup.ErrExportRead)
	}

	defer rows.Close()

	for rows.Next() {
		var (
			row       registrystore.KeyRow
			namespace *string
		)

		scanErr := rows.Scan(
			&namespace,
			&row.Fingerprint,
			&row.ArmoredKey,
			&row.AddedBy,
			&row.AddedAtMs,
		)
		if scanErr != nil {
			return fmt.Errorf("read staged registry keys: %w", backup.ErrExportRead)
		}

		// A key of a namespace that the backup does not hold is corrupt.
		if namespace == nil {
			return fmt.Errorf("staged registry key %q has no namespace: %w", row.Fingerprint, backup.ErrCorrupt)
		}

		row.Namespace = *namespace

		if _, keyErr := registrystore.Key(row); keyErr != nil {
			return fmt.Errorf("staged registry key %q is not valid: %w: %w", row.Fingerprint, keyErr, backup.ErrCorrupt)
		}
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		return fmt.Errorf("read staged registry keys: %w", backup.ErrExportRead)
	}

	return nil
}

// checkStagedRegistry checks the release rows and the key rows of a staged
// backup.
func checkStagedRegistry(ctx context.Context, pool *pgxpool.Pool, staging string) error {
	if err := checkStagedRegistryReleases(ctx, pool, staging); err != nil {
		return err
	}

	return checkStagedRegistryKeys(ctx, pool, staging)
}

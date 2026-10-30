package postgres

import (
	"context"
	"fmt"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/store/internal/blobrefs"
	"opendustpan.org/opendustpan/pkg/store/internal/registrystore"
)

// addStoredBlobRefs adds the blob references of the rows of job plans, of job
// log chunks and of registry releases to c. table maps a table name to the
// name that the query uses: the quoted name for the live tables, and the name
// in the staging schema for a staged backup. A row without the size and the
// MD5 of the stored bytes goes to the list of blobs that the backup does not
// copy.
func addStoredBlobRefs(ctx context.Context, q pgQuerier, table func(string) string, c *blobrefs.Collector) error {
	if err := addPlanBlobRefs(ctx, q, table, c); err != nil {
		return err
	}

	if err := addChunkBlobRefs(ctx, q, table, c); err != nil {
		return err
	}

	return addReleaseBlobRefs(ctx, q, table, c)
}

// addPlanBlobRefs adds the parts of each stored plan. The item key of a
// shredded plan has no blob.
func addPlanBlobRefs(ctx context.Context, q pgQuerier, table func(string) string, c *blobrefs.Collector) error {
	rows, err := q.Query(ctx, "SELECT p.namespace_id, p.job_id, p.id, p.body, COALESCE(k.shredded_at_ms, 0) > 0 FROM "+
		table("job_plans")+" AS p LEFT JOIN "+table("item_keys")+
		" AS k ON k.namespace_id = p.namespace_id AND k.kind = 'plan' AND k.item = p.id ORDER BY p.id")
	if err != nil {
		return fmt.Errorf("read backup plan blob references: %w", err)
	}

	defer rows.Close()

	for rows.Next() {
		var row blobrefs.PlanRow

		if scanErr := rows.Scan(&row.NamespaceID, &row.JobID, &row.ID, &row.Body, &row.Shredded); scanErr != nil {
			return fmt.Errorf("read backup plan blob references: %w", scanErr)
		}

		if addErr := c.Plan(row); addErr != nil {
			return addErr
		}
	}

	if iterErr := rows.Err(); iterErr != nil {
		return fmt.Errorf("read backup plan blob references: %w", iterErr)
	}

	return nil
}

// addChunkBlobRefs adds each confirmed log chunk. The job of a chunk comes from
// its log version row. The item key of a shredded log version has no blob.
func addChunkBlobRefs(ctx context.Context, q pgQuerier, table func(string) string, c *blobrefs.Collector) error {
	rows, err := q.Query(ctx, "SELECT c.namespace_id, l.job_id, c.log_id, c.seq, c.confirmed = 1, "+
		"COALESCE(k.shredded_at_ms, 0) > 0, c.stored_size, c.stored_md5, c.blob_key FROM "+table("job_log_chunks")+
		" AS c LEFT JOIN "+table("job_logs")+" AS l ON l.id = c.log_id AND l.namespace_id = c.namespace_id LEFT JOIN "+
		table("item_keys")+" AS k ON k.namespace_id = c.namespace_id AND k.kind = 'job-log' AND k.item = c.log_id "+
		"ORDER BY c.log_id, c.seq")
	if err != nil {
		return fmt.Errorf("read backup chunk blob references: %w", err)
	}

	defer rows.Close()

	for rows.Next() {
		var (
			row   blobrefs.ChunkRow
			jobID *string
		)

		scanErr := rows.Scan(
			&row.NamespaceID, &jobID, &row.LogID, &row.Seq, &row.Confirmed, &row.Shredded, &row.StoredSize,
			&row.StoredMD5, &row.BlobKey,
		)
		if scanErr != nil {
			return fmt.Errorf("read backup chunk blob references: %w", scanErr)
		}

		if jobID == nil {
			return fmt.Errorf("log chunk %d of log %q has no log version: %w", row.Seq, row.LogID, backup.ErrCorrupt)
		}

		row.JobID = *jobID

		if addErr := c.Chunk(row); addErr != nil {
			return addErr
		}
	}

	if iterErr := rows.Err(); iterErr != nil {
		return fmt.Errorf("read backup chunk blob references: %w", iterErr)
	}

	return nil
}

// addReleaseBlobRefs adds the files of each registry release.
func addReleaseBlobRefs(ctx context.Context, q pgQuerier, table func(string) string, c *blobrefs.Collector) error {
	rows, err := q.Query(ctx, "SELECT r.namespace_id, n.name, r.kind, r.name, r.system, r.version, r.sums_file, "+
		"r.sums_sha256, r.signature_file, r.signature_sha256, r.key_fingerprint, r.protocols, r.published_by, "+
		"r.archives_json, r.published_at_ms, r.stored_files_json FROM "+table("registry_releases")+" AS r JOIN "+
		table(
			"namespaces",
		)+" AS n ON n.id = r.namespace_id ORDER BY r.namespace_id, r.kind, r.name, r.system, r.version")
	if err != nil {
		return fmt.Errorf("read backup registry blob references: %w", err)
	}

	defer rows.Close()

	for rows.Next() {
		var (
			nsID string
			row  registrystore.Row
		)

		scanErr := rows.Scan(
			&nsID, &row.Namespace, &row.Kind, &row.Name, &row.System, &row.Version, &row.SumsFile, &row.SumsSHA256,
			&row.SignatureFile, &row.SignatureSHA256, &row.KeyFingerprint, &row.Protocols, &row.PublishedBy,
			&row.ArchivesJSON, &row.PublishedAtMs, &row.StoredFilesJSON,
		)
		if scanErr != nil {
			return fmt.Errorf("read backup registry blob references: %w", scanErr)
		}

		rel, relErr := registrystore.Release(row)
		if relErr != nil {
			return fmt.Errorf("registry release %s/%s %s@%s (%s): %w: %w",
				row.Namespace, row.Kind, row.Name, row.Version, row.System, relErr, backup.ErrCorrupt)
		}

		if addErr := c.Release(blobrefs.ReleaseRow{NamespaceID: nsID, Release: rel}); addErr != nil {
			return addErr
		}
	}

	if iterErr := rows.Err(); iterErr != nil {
		return fmt.Errorf("read backup registry blob references: %w", iterErr)
	}

	return nil
}

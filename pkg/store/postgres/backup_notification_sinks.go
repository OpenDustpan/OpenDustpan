package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/store/internal/sinkstore"
)

// Column names of the sink tables that other tables share.
const (
	sinkColRef        = "ref"
	sinkColCiphertext = "ciphertext"
)

// pgCopySinkTables holds the COPY columns of the notification sink tables, in
// the order of backup.Tables.
func pgCopySinkTables() []pgCopyTable {
	return []pgCopyTable{
		{
			table: "notification_sinks",
			columns: []string{
				"id", pgColNamespaceID, pgColKind, pgColName, "platform", "target", pgColCreatedBy, pgColVersion,
			},
			orderBy: []string{"id"},
		},
		{
			table:   "notification_sink_secrets",
			columns: []string{pgColNamespaceID, sinkColRef, "sink_id", pgColPosition, sinkColCiphertext},
			orderBy: []string{pgColNamespaceID, sinkColRef},
		},
		{
			table:   "notification_sink_refs",
			columns: []string{pgColNamespaceID, sinkColRef},
			orderBy: []string{pgColNamespaceID, sinkColRef},
		},
	}
}

// checkStagedSinks checks the sink tables of a staged backup with the rules of
// a stored sink. A row that fails makes the backup corrupt. It returns
// backup.ErrCorrupt.
func checkStagedSinks(ctx context.Context, pool *pgxpool.Pool, staging string) error {
	sinks, err := stagedSinkRows(ctx, pool, staging)
	if err != nil {
		return err
	}

	secrets, err := stagedSinkSecrets(ctx, pool, staging)
	if err != nil {
		return err
	}

	held, err := stagedSinkRefs(ctx, pool, staging)
	if err != nil {
		return err
	}

	if checkErr := sinkstore.CheckStaged(sinks, secrets, held); checkErr != nil {
		return fmt.Errorf("staged notification sinks: %w: %w", checkErr, backup.ErrCorrupt)
	}

	return nil
}

func stagedSinkRows(ctx context.Context, pool *pgxpool.Pool, staging string) ([]sinkstore.Row, error) {
	query := "SELECT k.id, n.name, k.kind, k.name, k.platform, k.target, k.created_by, k.version FROM " +
		stagingTable(staging, "notification_sinks") + " AS k LEFT JOIN " + stagingTable(staging, "namespaces") +
		` AS n ON n.id = k.namespace_id ORDER BY k.id COLLATE "C"`

	rows, err := pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("read staged sinks: %w", backup.ErrExportRead)
	}

	defer rows.Close()

	var out []sinkstore.Row

	for rows.Next() {
		var (
			row       sinkstore.Row
			namespace *string
		)

		scanErr := rows.Scan(&row.ID, &namespace, &row.Kind, &row.Name, &row.Platform, &row.Target, &row.CreatedBy,
			&row.Version)
		if scanErr != nil {
			return nil, fmt.Errorf("read staged sinks: %w", backup.ErrExportRead)
		}

		// A sink of a namespace that the backup does not hold is corrupt. The
		// LEFT JOIN keeps such a row, and the NULL name fails it.
		if namespace == nil {
			return nil, fmt.Errorf("staged sink %q has no namespace: %w", row.ID, backup.ErrCorrupt)
		}

		row.Namespace = *namespace
		out = append(out, row)
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, fmt.Errorf("read staged sinks: %w", backup.ErrExportRead)
	}

	return out, nil
}

func stagedSinkSecrets(ctx context.Context, pool *pgxpool.Pool, staging string) ([]sinkstore.Secret, error) {
	query := "SELECT n.name, s.ref, s.sink_id FROM " + stagingTable(staging, "notification_sink_secrets") +
		" AS s LEFT JOIN " + stagingTable(staging, "namespaces") +
		` AS n ON n.id = s.namespace_id ORDER BY s.sink_id COLLATE "C", s.position`

	rows, err := pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("read staged sink secrets: %w", backup.ErrExportRead)
	}

	defer rows.Close()

	var out []sinkstore.Secret

	for rows.Next() {
		var (
			sec       sinkstore.Secret
			namespace *string
		)

		if scanErr := rows.Scan(&namespace, &sec.Ref, &sec.SinkID); scanErr != nil {
			return nil, fmt.Errorf("read staged sink secrets: %w", backup.ErrExportRead)
		}

		if namespace == nil {
			return nil, fmt.Errorf("staged sink secret %q has no namespace: %w", sec.Ref, backup.ErrCorrupt)
		}

		sec.Namespace = *namespace
		out = append(out, sec)
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, fmt.Errorf("read staged sink secrets: %w", backup.ErrExportRead)
	}

	return out, nil
}

func stagedSinkRefs(ctx context.Context, pool *pgxpool.Pool, staging string) (map[string]bool, error) {
	query := "SELECT n.name, r.ref FROM " + stagingTable(staging, "notification_sink_refs") + " AS r LEFT JOIN " +
		stagingTable(staging, "namespaces") + " AS n ON n.id = r.namespace_id"

	rows, err := pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("read staged sink refs: %w", backup.ErrExportRead)
	}

	defer rows.Close()

	held := map[string]bool{}

	for rows.Next() {
		var (
			ref       string
			namespace *string
		)

		if scanErr := rows.Scan(&namespace, &ref); scanErr != nil {
			return nil, fmt.Errorf("read staged sink refs: %w", backup.ErrExportRead)
		}

		if namespace == nil {
			return nil, fmt.Errorf("staged sink ref %q has no namespace: %w", ref, backup.ErrCorrupt)
		}

		held[*namespace+"/"+ref] = true
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, fmt.Errorf("read staged sink refs: %w", backup.ErrExportRead)
	}

	return held, nil
}

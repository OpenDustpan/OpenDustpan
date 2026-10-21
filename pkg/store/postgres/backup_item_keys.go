package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/internal/jobstore"
)

// pgCopyItemKeyTables holds the COPY columns of the item key table, in the
// order of backup.Tables.
func pgCopyItemKeyTables() []pgCopyTable {
	return []pgCopyTable{
		{
			table: "item_keys",
			columns: []string{
				"id", pgColNamespaceID, pgColScopeID, pgColKind, "item", pgColWrapperID, pgColCreatedAtMs,
				"shredded_at_ms",
			},
			orderBy: []string{"id"},
		},
	}
}

// checkStagedOwnedRows checks the rows of a staged backup that belong to a
// namespace and carry a rule of their own: the saved views and the item keys.
func checkStagedOwnedRows(ctx context.Context, pool *pgxpool.Pool, staging string) error {
	if err := checkStagedSavedViews(ctx, pool, staging); err != nil {
		return err
	}

	if err := checkStagedItemKeys(ctx, pool, staging); err != nil {
		return err
	}

	return checkStagedJobArtifacts(ctx, pool, staging)
}

// checkStagedItemKeys checks each item key row of a staged backup with the
// rules of a new record. A row that fails makes the backup corrupt, so a
// restore never brings in a record that the server would not have stored. It
// returns backup.ErrCorrupt.
//
// Defense in depth: the store checks a row again when it reads it. This check
// stays so that a bad row fails the restore and does not wait for a reader.
func checkStagedItemKeys(ctx context.Context, pool *pgxpool.Pool, staging string) error {
	query := "SELECT k.id, n.name, k.scope_id, k.kind, k.item, k.wrapper_id, k.created_at_ms, k.shredded_at_ms FROM " +
		stagingTable(staging, "item_keys") + " AS k LEFT JOIN " + stagingTable(staging, "namespaces") +
		" AS n ON n.id = k.namespace_id"

	rows, err := pool.Query(ctx, query)
	if err != nil {
		return fmt.Errorf("read staged item keys: %w", backup.ErrExportRead)
	}

	defer rows.Close()

	for rows.Next() {
		var (
			row       codec.ItemKeyRow
			namespace *string
		)

		scanErr := rows.Scan(&row.ID, &namespace, &row.ScopeID, &row.Kind, &row.Item, &row.WrapperID,
			&row.CreatedAtMs, &row.ShreddedAtMs)
		if scanErr != nil {
			return fmt.Errorf("read staged item keys: %w", backup.ErrExportRead)
		}

		// A record of a namespace that the backup does not hold is corrupt. The
		// LEFT JOIN keeps such a row in the result, and the NULL name fails it.
		if namespace == nil {
			return fmt.Errorf("staged item key %q has no namespace: %w", row.ID, backup.ErrCorrupt)
		}

		row.Namespace = *namespace

		if _, keyErr := codec.FromItemKeyRow(row); keyErr != nil {
			return fmt.Errorf("staged item key %q is not valid: %w: %w", row.ID, keyErr, backup.ErrCorrupt)
		}
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		return fmt.Errorf("read staged item keys: %w", backup.ErrExportRead)
	}

	return nil
}

// checkStagedJobArtifacts checks the rows of plans, log versions and log chunks
// of a staged backup: each row names a namespace that the backup holds, and
// each plan body is the plan of its row. A row that fails makes the backup
// corrupt. It returns backup.ErrCorrupt.
//
// Defense in depth: the store checks a plan again when it reads it. This check
// stays so that a bad row fails the restore and does not wait for a reader.
func checkStagedJobArtifacts(ctx context.Context, pool *pgxpool.Pool, staging string) error {
	for _, table := range []string{"job_logs", "job_log_chunks"} {
		var orphans int64

		query := "SELECT COUNT(*) FROM " + stagingTable(staging, table) + " AS t LEFT JOIN " +
			stagingTable(staging, "namespaces") + " AS n ON n.id = t.namespace_id WHERE n.id IS NULL"
		if err := pool.QueryRow(ctx, query).Scan(&orphans); err != nil {
			return fmt.Errorf("read staged %s: %w", table, backup.ErrExportRead)
		}

		if orphans > 0 {
			return fmt.Errorf("staged %s has %d rows with no namespace: %w", table, orphans, backup.ErrCorrupt)
		}
	}

	query := "SELECT p.id, n.id, p.body FROM " + stagingTable(staging, "job_plans") + " AS p LEFT JOIN " +
		stagingTable(staging, "namespaces") + " AS n ON n.id = p.namespace_id"

	rows, err := pool.Query(ctx, query)
	if err != nil {
		return fmt.Errorf("read staged plans: %w", backup.ErrExportRead)
	}

	defer rows.Close()

	for rows.Next() {
		var (
			id, body string
			ns       *string
		)

		if scanErr := rows.Scan(&id, &ns, &body); scanErr != nil {
			return fmt.Errorf("read staged plans: %w", backup.ErrExportRead)
		}

		if ns == nil {
			return fmt.Errorf("staged plan %q has no namespace: %w", id, backup.ErrCorrupt)
		}

		if bodyErr := jobstore.CheckPlanBody(id, body); bodyErr != nil {
			return fmt.Errorf("staged plan %q is not valid: %w: %w", id, bodyErr, backup.ErrCorrupt)
		}
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		return fmt.Errorf("read staged plans: %w", backup.ErrExportRead)
	}

	return nil
}

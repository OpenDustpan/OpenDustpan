package postgres

import (
	"context"
	"fmt"
	"slices"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/store/internal/sinkstore"
)

// Column names of the sink tables that other tables share.
const (
	sinkColScope      = "scope"
	sinkColSinkID     = "sink_id"
	sinkColPrincipal  = "principal"
	sinkColSubject    = "subject"
	sinkColRef        = "ref"
	sinkColCiphertext = "ciphertext"
)

// pgCopySinkTables holds the COPY columns of the notification sink tables, in
// the order of backup.Tables.
func pgCopySinkTables() []pgCopyTable {
	return slices.Concat([]pgCopyTable{
		{
			table: "notification_sinks",
			columns: []string{
				"id", sinkColScope, pgColKind, pgColName, "platform", "target", pgColCreatedBy, pgColVersion,
			},
			orderBy: []string{"id"},
		},
		{
			table:   "notification_sink_secrets",
			columns: []string{sinkColScope, sinkColRef, sinkColSinkID, pgColPosition, sinkColCiphertext},
			orderBy: []string{sinkColScope, sinkColRef},
		},
		{
			table:   "notification_sink_refs",
			columns: []string{sinkColScope, sinkColRef},
			orderBy: []string{sinkColScope, sinkColRef},
		},
	}, pgCopyOutboxTables())
}

// pgCopyOutboxTables holds the backup columns of the outbox, the subscription and the
// delivery tables, in the order of backup.Tables.
func pgCopyOutboxTables() []pgCopyTable {
	return []pgCopyTable{
		{
			table: "notifications",
			columns: []string{
				sinkColScope, pgColSequence, "id", "type", "object", "object_id", sinkColSubject, "agent", "severity",
				"summary", "links", "attributes", "recipients", "dedupe_key", "at_ms",
			},
			orderBy: []string{sinkColScope, pgColSequence},
		},
		{
			table:   "notification_sequences",
			columns: []string{sinkColScope, "last_sequence"},
			orderBy: []string{sinkColScope},
		},
		{
			table:   "notification_cursors",
			columns: []string{pgColName, sinkColScope, pgColSequence},
			orderBy: []string{pgColName, sinkColScope},
		},
		{
			table: "notification_subscriptions",
			columns: []string{
				"id",
				sinkColScope,
				sinkColSinkID,
				"types",
				"objects",
				"min_severity",
				pgColState,
				"reason",
				pgColCreatedBy,
				pgColVersion,
			},
			orderBy: []string{"id"},
		},
		{
			table: "notification_deliveries",
			columns: []string{
				"id",
				sinkColScope,
				"notification_id",
				"subscription_id",
				sinkColSinkID,
				"contact",
				sinkColPrincipal,
				pgColState,
				"attempts",
				pgColCreatedAtMs,
				"next_at_ms",
				"last_error",
				"delivered_at_ms",
				"owner",
				"lease_until_ms",
				pgColVersion,
			},
			orderBy: []string{"id"},
		},
	}
}

// checkStagedSinks checks the sink tables of a staged backup with the rules of
// a stored sink. A row that fails makes the backup corrupt. It returns
// backup.ErrCorrupt.
func checkStagedSinks(ctx context.Context, pool pgQuerier, staging string) error {
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

func stagedSinkRows(ctx context.Context, pool pgQuerier, staging string) ([]sinkstore.Row, error) {
	query := "SELECT id, scope, kind, name, platform, target, created_by, version FROM " +
		stagingTable(staging, "notification_sinks") + ` ORDER BY id COLLATE "C"`

	rows, err := pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("read staged sinks: %w", backup.ErrExportRead)
	}

	defer rows.Close()

	var out []sinkstore.Row

	for rows.Next() {
		var row sinkstore.Row

		scanErr := rows.Scan(&row.ID, &row.Namespace, &row.Kind, &row.Name, &row.Platform, &row.Target,
			&row.CreatedBy, &row.Version)
		if scanErr != nil {
			return nil, fmt.Errorf("read staged sinks: %w", backup.ErrExportRead)
		}

		// A sink of a scope that the backup does not hold is corrupt:
		// checkStagedNotifications refuses it.
		out = append(out, row)
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, fmt.Errorf("read staged sinks: %w", backup.ErrExportRead)
	}

	return out, nil
}

func stagedSinkSecrets(ctx context.Context, pool pgQuerier, staging string) ([]sinkstore.Secret, error) {
	query := "SELECT scope, ref, sink_id FROM " + stagingTable(staging, "notification_sink_secrets") +
		` ORDER BY sink_id COLLATE "C", position`

	rows, err := pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("read staged sink secrets: %w", backup.ErrExportRead)
	}

	defer rows.Close()

	var out []sinkstore.Secret

	for rows.Next() {
		var sec sinkstore.Secret

		if scanErr := rows.Scan(&sec.Namespace, &sec.Ref, &sec.SinkID); scanErr != nil {
			return nil, fmt.Errorf("read staged sink secrets: %w", backup.ErrExportRead)
		}

		out = append(out, sec)
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, fmt.Errorf("read staged sink secrets: %w", backup.ErrExportRead)
	}

	return out, nil
}

func stagedSinkRefs(ctx context.Context, pool pgQuerier, staging string) (map[string]bool, error) {
	query := "SELECT scope, ref FROM " + stagingTable(staging, "notification_sink_refs")

	rows, err := pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("read staged sink refs: %w", backup.ErrExportRead)
	}

	defer rows.Close()

	held := map[string]bool{}

	for rows.Next() {
		var scope, ref string

		if scanErr := rows.Scan(&scope, &ref); scanErr != nil {
			return nil, fmt.Errorf("read staged sink refs: %w", backup.ErrExportRead)
		}

		held[scope+"/"+ref] = true
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, fmt.Errorf("read staged sink refs: %w", backup.ErrExportRead)
	}

	return held, nil
}

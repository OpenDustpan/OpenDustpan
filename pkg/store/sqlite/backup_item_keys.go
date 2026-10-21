package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
)

// sqliteItemKeyBackupTables holds the backup columns of the item key table,
// in the order of backup.Tables.
func sqliteItemKeyBackupTables() []sqliteBackupTable {
	return []sqliteBackupTable{
		{
			table: "item_keys",
			columns: []string{
				"id", backupColNamespaceID, "scope_id", backupColKind, "item", backupColWrapperID,
				backupColCreatedAtMs, "shredded_at_ms", "blob_version", "blob_size", "blob_md5",
			},
		},
	}
}

// checkStagedOwnedRows checks the rows of a staged backup that belong to a
// namespace and carry a rule of their own: the saved views and the item keys.
func checkStagedOwnedRows(ctx context.Context, conn *sql.DB) error {
	if err := checkStagedSavedViews(ctx, conn); err != nil {
		return err
	}

	return checkStagedItemKeys(ctx, conn)
}

// checkStagedItemKeys checks each item key row of a staged backup with the
// rules of a new record. A row that fails makes the backup corrupt, so a
// restore never brings in a record that the server would not have stored. It
// returns backup.ErrCorrupt.
//
// Defense in depth: the store checks a row again when it reads it. This check
// stays so that a bad row fails the restore and does not wait for a reader.
func checkStagedItemKeys(ctx context.Context, conn *sql.DB) error {
	const query = `SELECT k.id, n.name, k.scope_id, k.kind, k.item, k.wrapper_id, k.created_at_ms, k.shredded_at_ms,
	k.blob_version, k.blob_size, k.blob_md5
FROM item_keys AS k LEFT JOIN namespaces AS n ON n.id = k.namespace_id`

	rows, err := conn.QueryContext(ctx, query)
	if err != nil {
		return fmt.Errorf("read staged item keys: %w", backup.ErrExportRead)
	}

	defer func() {
		_ = rows.Close() //nolint:errcheck // Read-only check handle.
	}()

	for rows.Next() {
		var (
			row       codec.ItemKeyRow
			namespace sql.NullString
		)

		scanErr := rows.Scan(&row.ID, &namespace, &row.ScopeID, &row.Kind, &row.Item, &row.WrapperID,
			&row.CreatedAtMs, &row.ShreddedAtMs, &row.BlobVersion, &row.BlobSize, &row.BlobMd5)
		if scanErr != nil {
			return fmt.Errorf("read staged item keys: %w", backup.ErrExportRead)
		}

		// A record of a namespace that the backup does not hold is corrupt. The
		// LEFT JOIN keeps such a row in the result, and the NULL name fails it.
		if !namespace.Valid {
			return fmt.Errorf("staged item key %q has no namespace: %w", row.ID, backup.ErrCorrupt)
		}

		row.Namespace = namespace.String

		if _, keyErr := codec.FromItemKeyRow(row); keyErr != nil {
			return fmt.Errorf("staged item key %q is not valid: %w: %w", row.ID, keyErr, backup.ErrCorrupt)
		}
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		return fmt.Errorf("read staged item keys: %w", backup.ErrExportRead)
	}

	return nil
}

package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/backup"
)

// keepLiveSettings replaces, in the staged schema, the row of each key of the
// installation settings with the live row, in tx. A key that the live schema
// holds no row for loses its staged row. A key that backup.IsKeptSettingKey
// does not name returns backup.ErrCorrupt before any write. Each query uses
// placeholders for the values; the schema names come from the swap plan.
func keepLiveSettings(ctx context.Context, tx pgx.Tx, live, staging string, keys []string) error {
	if len(keys) == 0 {
		return nil
	}

	for _, key := range keys {
		if !backup.IsKeptSettingKey(key) {
			return fmt.Errorf("keep setting row %q: %w", key, backup.ErrCorrupt)
		}
	}

	stagedTable := pgIdent(staging) + ".settings"
	liveTable := pgIdent(live) + ".settings"

	for _, key := range keys {
		_, err := tx.Exec(ctx, "DELETE FROM "+stagedTable+" WHERE scope_id = $1 AND key = $2", serverScopeID, key)
		if err != nil {
			return fmt.Errorf("clear staged setting row %q: %w", key, backup.ErrExportRead)
		}

		insert := "INSERT INTO " + stagedTable + " (scope_id, key, value, updated_at_ms) " +
			"SELECT scope_id, key, value, updated_at_ms FROM " + liveTable + " WHERE scope_id = $1 AND key = $2"

		if _, err = tx.Exec(ctx, insert, serverScopeID, key); err != nil {
			return fmt.Errorf("keep setting row %q: %w", key, backup.ErrExportRead)
		}
	}

	return nil
}

// swappedSettingText returns the text of the restore access setting in the
// live schema of tx, or the default when the schema has no row for the key.
func swappedSettingText(ctx context.Context, tx pgx.Tx) (string, error) {
	var value string

	err := tx.QueryRow(
		ctx, `SELECT value FROM settings WHERE scope_id = $1 AND key = $2`,
		serverScopeID, backup.SettingRestoreAccessTables,
	).Scan(&value)

	switch {
	case err == nil:
		return value, nil
	case errors.Is(err, pgx.ErrNoRows):
		return backup.RestoreAccessSettingDefault, nil
	default:
		return "", fmt.Errorf("read swapped setting row: %w", backup.ErrExportRead)
	}
}

// withSwappedSetting returns d with the value of the restore access setting
// after the swap, read from the live schema in the swap transaction, after the
// swap and before the step record. A swap that keeps the live row records no
// value.
func withSwappedSetting(ctx context.Context, tx pgx.Tx, d backup.SwapDecision) (backup.SwapDecision, error) {
	d.SettingAfter = ""

	if len(d.KeepSettingKeys) > 0 {
		return d, nil
	}

	value, err := swappedSettingText(ctx, tx)
	if err != nil {
		return d, err
	}

	d.SettingAfter = value

	return d, nil
}

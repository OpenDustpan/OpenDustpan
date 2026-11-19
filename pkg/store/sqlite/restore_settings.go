package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"opendustpan.org/opendustpan/pkg/backup"
)

// liveSettingRow is the live row of one key of the installation settings, or
// the absence of it.
type liveSettingRow struct {
	key         string
	value       string
	updatedAtMs int64
	found       bool
}

// keepLiveSettings replaces, in the staged file at path, the row of each key
// of the installation settings with the live row. A key that the live
// database holds no row for loses its staged row, so the restored value never
// stays when the live value is the default. The caller holds the write lock.
// A key that backup.IsKeptSettingKey does not name returns backup.ErrCorrupt
// before any write. Each query uses placeholders.
func (s *Store) keepLiveSettings(ctx context.Context, path string, keys []string) error {
	if len(keys) == 0 {
		return nil
	}

	for _, key := range keys {
		if !backup.IsKeptSettingKey(key) {
			return fmt.Errorf("keep setting row %q: %w", key, backup.ErrCorrupt)
		}
	}

	rows, err := s.readLiveSettingRows(ctx, keys)
	if err != nil {
		return err
	}

	conn, err := openStagedAccess(ctx, path)
	if err != nil {
		return err
	}

	defer func() {
		if closeErr := conn.Close(); closeErr != nil {
			_ = closeErr // Best-effort close of the staging handle.
		}
	}()

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("keep setting rows: %w", backup.ErrExportRead)
	}

	if err := writeStagedSettingRows(ctx, tx, rows); err != nil {
		// Best-effort rollback keeps the staged file as it was; err is the cause.
		return errors.Join(err, tx.Rollback())
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("keep setting rows: %w", backup.ErrExportRead)
	}

	return nil
}

// readLiveSettingRows reads the live row of each key of the installation.
func (s *Store) readLiveSettingRows(ctx context.Context, keys []string) ([]liveSettingRow, error) {
	rows := make([]liveSettingRow, 0, len(keys))

	for _, key := range keys {
		row := liveSettingRow{key: key, value: "", updatedAtMs: 0, found: false}

		err := s.conn.QueryRowContext(
			ctx, `SELECT value, updated_at_ms FROM settings WHERE scope_id = ? AND key = ?`, serverScopeID, key,
		).Scan(&row.value, &row.updatedAtMs)

		switch {
		case err == nil:
			row.found = true
		case errors.Is(err, sql.ErrNoRows):
			row.found = false
		default:
			return nil, fmt.Errorf("read live setting row %q: %w", key, backup.ErrExportRead)
		}

		rows = append(rows, row)
	}

	return rows, nil
}

// writeStagedSettingRows deletes the staged row of each key of rows, then
// inserts the live row when there is one.
func writeStagedSettingRows(ctx context.Context, tx *sql.Tx, rows []liveSettingRow) error {
	for _, row := range rows {
		_, err := tx.ExecContext(ctx, `DELETE FROM settings WHERE scope_id = ? AND key = ?`, serverScopeID, row.key)
		if err != nil {
			return fmt.Errorf("clear staged setting row %q: %w", row.key, backup.ErrExportRead)
		}

		if !row.found {
			continue
		}

		_, err = tx.ExecContext(
			ctx, `INSERT INTO settings (scope_id, key, value, updated_at_ms) VALUES (?, ?, ?, ?)`,
			serverScopeID, row.key, row.value, row.updatedAtMs,
		)
		if err != nil {
			return fmt.Errorf("keep setting row %q: %w", row.key, backup.ErrExportRead)
		}
	}

	return nil
}

// stagedSettingText returns the text of the restore access setting in the
// staged file of tx, or the default when the file has no row for the key.
func stagedSettingText(ctx context.Context, tx *sql.Tx) (string, error) {
	var value string

	err := tx.QueryRowContext(
		ctx, `SELECT value FROM main.settings WHERE scope_id = ? AND key = ?`,
		serverScopeID, backup.SettingRestoreAccessTables,
	).Scan(&value)

	switch {
	case err == nil:
		return value, nil
	case errors.Is(err, sql.ErrNoRows):
		return backup.RestoreAccessSettingDefault, nil
	default:
		return "", fmt.Errorf("read staged setting row: %w", backup.ErrExportRead)
	}
}

// withStagedSetting returns d with the value of the restore access setting
// after the swap, read from the file that becomes live, in the transaction
// that records the step. A swap that keeps the live row records no value.
func withStagedSetting(ctx context.Context, tx *sql.Tx, d backup.SwapDecision) (backup.SwapDecision, error) {
	d.SettingAfter = ""

	if len(d.KeepSettingKeys) > 0 {
		return d, nil
	}

	value, err := stagedSettingText(ctx, tx)
	if err != nil {
		return d, err
	}

	d.SettingAfter = value

	return d, nil
}

package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/backup"
)

// checkStagedRemovalAlerts checks each alert marker row of a staged backup: a
// marker with a time after the time of the restore makes the backup corrupt.
// A zero restore time refuses every marker row. It returns backup.ErrCorrupt.
//
// Defense in depth: the removal alert service treats a marker after the time
// of the removal as no marker. This check stays so that a restore never
// brings in a marker that silences the alert.
func checkStagedRemovalAlerts(ctx context.Context, conn *sql.DB, restoreAt time.Time) (err error) {
	rows, err := conn.QueryContext(
		ctx, `SELECT source_kind, source_id, alerted_at_ms FROM member_removal_alerts`,
	)
	if err != nil {
		return fmt.Errorf("read staged removal alert markers: %w", backup.ErrExportRead)
	}

	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close staged removal alert markers: %w", closeErr))
		}
	}()

	for rows.Next() {
		var (
			sourceKind, sourceID string
			alertedAtMs          int64
		)

		scanErr := rows.Scan(&sourceKind, &sourceID, &alertedAtMs)
		if scanErr != nil {
			return fmt.Errorf("read staged removal alert markers: %w", backup.ErrExportRead)
		}

		if restoreAt.IsZero() {
			return fmt.Errorf(
				"staged removal alert marker of source %s %q has no time of the restore: %w",
				sourceKind,
				sourceID,
				backup.ErrCorrupt,
			)
		}

		if alertedAtMs > restoreAt.UnixMilli() {
			return fmt.Errorf(
				"staged removal alert marker of source %s %q is after the time of the restore: %w",
				sourceKind,
				sourceID,
				backup.ErrCorrupt,
			)
		}
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		return fmt.Errorf("read staged removal alert markers: %w", backup.ErrExportRead)
	}

	return nil
}

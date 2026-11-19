package postgres

import (
	"context"
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

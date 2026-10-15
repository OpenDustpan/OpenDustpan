package sqlite

import (
	"context"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/sqlite/internal/db"
)

// maxExpiredList bounds one page of ListExpiredDeleted.
const maxExpiredList = 10000

var _ inventory.ExpiredDeleted = (*Store)(nil)

// ListExpiredDeleted implements inventory.ExpiredDeleted with one fixed
// query for the first page and one for the page after a cursor.
func (s *Store) ListExpiredDeleted(
	ctx context.Context, now time.Time, after *inventory.ExpiredCursor, limit int,
) ([]inventory.DeletedEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	size := int64(max(0, min(limit, maxExpiredList)))

	var (
		rows []db.InventoryDeleted
		err  error
	)

	if after == nil {
		rows, err = s.queries.ListExpiredInventory(ctx, db.ListExpiredInventoryParams{
			PurgeAfterMs: now.UnixMilli(), Limit: size,
		})
	} else {
		rows, err = s.queries.ListExpiredInventoryAfter(ctx, db.ListExpiredInventoryAfterParams{
			NowMs: now.UnixMilli(), AfterMs: after.PurgeAfter.UnixMilli(), AfterTool: after.Tool,
			AfterUnitID: after.UnitID, PageLimit: size,
		})
	}

	if err != nil {
		return nil, fmt.Errorf("select expired deleted inventory: %w", err)
	}

	out := make([]inventory.DeletedEntry, 0, len(rows))
	for i := range rows {
		out = append(out, inventory.DeletedEntry{
			DeletedAt:  time.UnixMilli(rows[i].DeletedAtMs).UTC(),
			PurgeAfter: time.UnixMilli(rows[i].PurgeAfterMs).UTC(),
			Namespace:  namespace.Name(rows[i].Namespace),
			Tool:       rows[i].Tool,
			UnitID:     rows[i].UnitID,
			Name:       rows[i].Name,
		})
	}

	return out, nil
}

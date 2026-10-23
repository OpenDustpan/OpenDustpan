package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// QueryWorkspaces implements workspace.Store with one count and one page
// query in one REPEATABLE READ snapshot, so the total agrees with the page.
// The search compares characters exactly, as strings.Contains does.
func (s *Store) QueryWorkspaces(ctx context.Context, q workspace.ListQuery) (workspace.ListPage, error) {
	page := workspace.ListPage{Workspaces: []workspace.Workspace{}, Total: 0}

	opts := pgx.TxOptions{
		IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly, DeferrableMode: pgx.NotDeferrable, BeginQuery: "",
		CommitQuery: "",
	}

	err := pgx.BeginTxFunc(ctx, s.pool, opts, func(tx pgx.Tx) error {
		qtx := s.queries.WithTx(tx)

		total, err := qtx.CountWorkspacePage(ctx, db.CountWorkspacePageParams{
			Namespace: q.Namespace.String(), NameContains: q.NameContains,
		})
		if err != nil {
			return fmt.Errorf("count workspaces: %w", err)
		}

		page.Total = int(total)

		if q.PageOffset() >= page.Total {
			return nil
		}

		rows, err := qtx.ReadWorkspacePage(ctx, db.ReadWorkspacePageParams{
			Namespace:    q.Namespace.String(),
			NameContains: q.NameContains,
			PageOffset:   int32(q.PageOffset()), //nolint:gosec // The offset is below the total, which fits in int32.
			PageLimit:    int32(q.PageLimit()),  //nolint:gosec // PageLimit is at most MaxListLimit.
		})
		if err != nil {
			return fmt.Errorf("select workspace page: %w", err)
		}

		for i := range rows {
			page.Workspaces = append(page.Workspaces, workspaceFromRow(rows[i].Workspace, rows[i].NamespaceID))
		}

		return nil
	})
	if err != nil {
		return workspace.ListPage{}, fmt.Errorf("workspace page snapshot: %w", err)
	}

	return page, nil
}

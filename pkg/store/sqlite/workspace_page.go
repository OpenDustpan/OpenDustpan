package sqlite

import (
	"context"
	"fmt"

	"opendustpan.org/opendustpan/pkg/store/sqlite/internal/db"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// QueryWorkspaces implements workspace.Store with one count and one page
// query in one read transaction, so the total agrees with the page. The read
// transaction takes no write lock. The search compares characters exactly, as
// strings.Contains does: instr, unlike LIKE, does not fold the case of ASCII
// letters.
func (s *Store) QueryWorkspaces(ctx context.Context, q workspace.ListQuery) (workspace.ListPage, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var page workspace.ListPage

	err := s.inReadTx(ctx, func(qtx *db.Queries) error {
		var readErr error

		page, readErr = queryWorkspacePage(ctx, qtx, q)

		return readErr
	})
	if err != nil {
		return workspace.ListPage{}, err
	}

	return page, nil
}

// queryWorkspacePage counts the workspaces that match q and reads one page of
// them with qtx.
func queryWorkspacePage(ctx context.Context, qtx *db.Queries, q workspace.ListQuery) (workspace.ListPage, error) {
	page := workspace.ListPage{Workspaces: []workspace.Workspace{}, Total: 0}

	total, err := qtx.CountWorkspacePage(ctx, db.CountWorkspacePageParams{
		Namespace: q.Namespace.String(), NameContains: q.NameContains,
	})
	if err != nil {
		return workspace.ListPage{}, fmt.Errorf("count workspaces: %w", err)
	}

	page.Total = int(total)

	if q.PageOffset() >= page.Total {
		return page, nil
	}

	rows, err := qtx.ReadWorkspacePage(ctx, db.ReadWorkspacePageParams{
		Namespace:    q.Namespace.String(),
		NameContains: q.NameContains,
		PageOffset:   int64(q.PageOffset()),
		PageLimit:    int64(q.PageLimit()),
	})
	if err != nil {
		return workspace.ListPage{}, fmt.Errorf("select workspace page: %w", err)
	}

	for i := range rows {
		page.Workspaces = append(page.Workspaces, workspaceFromRow(rows[i].Workspace, rows[i].NamespaceID))
	}

	return page, nil
}

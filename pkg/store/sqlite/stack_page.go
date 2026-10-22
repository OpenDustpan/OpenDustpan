package sqlite

import (
	"context"
	"fmt"

	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/sqlite/internal/db"
)

// QueryStacks implements stack.Store. It reads the range of each namespace of
// q in the order of the page, with a range of the primary key, until the page
// is full, then the tags of the stacks of the page in one query.
func (s *Store) QueryStacks(ctx context.Context, q stack.PageQuery) ([]stack.Stack, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	limit := q.PageLimit()
	rows := []codec.StackRow{}

	for _, r := range q.Ranges() {
		need := limit - len(rows)
		if need == 0 {
			break
		}

		part, err := s.stackRange(ctx, r, q.Project, need)
		if err != nil {
			return nil, err
		}

		rows = append(rows, part...)
	}

	if len(rows) == 0 {
		return []stack.Stack{}, nil
	}

	ids := make([]string, len(rows))
	for i := range rows {
		ids[i] = rows[i].ID
	}

	tagRows, err := s.queries.ListStackTagsOfStacks(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("select stack page tags: %w", err)
	}

	tags := make(map[string]map[string]string, len(rows))

	for _, t := range tagRows {
		if tags[t.StackID] == nil {
			tags[t.StackID] = map[string]string{}
		}

		tags[t.StackID][t.Name] = t.Value
	}

	out := make([]stack.Stack, 0, len(rows))
	for i := range rows {
		out = append(out, codec.FromStackRow(rows[i], tags[rows[i].ID]))
	}

	return out, nil
}

// stackRange reads at most need stacks of the range r. A project that is
// not empty keeps the stacks of that project only.
func (s *Store) stackRange(
	ctx context.Context, r stack.NamespaceRange, project stack.ProjectName, need int,
) ([]codec.StackRow, error) {
	var rows []db.ListStackPageInNamespaceRow

	if project == "" {
		got, err := s.queries.ListStackPageInNamespace(ctx, db.ListStackPageInNamespaceParams{
			Namespace:    r.Namespace.String(),
			AfterProject: r.AfterProject.String(),
			AfterName:    r.AfterName.String(),
			PageLimit:    int64(need),
		})
		if err != nil {
			return nil, fmt.Errorf("select stack page: %w", err)
		}

		rows = got
	} else {
		got, err := s.queries.ListStackPageInProject(ctx, db.ListStackPageInProjectParams{
			Namespace: r.Namespace.String(),
			Project:   project.String(),
			AfterName: r.AfterName.String(),
			PageLimit: int64(need),
		})
		if err != nil {
			return nil, fmt.Errorf("select stack page: %w", err)
		}

		for i := range got {
			rows = append(rows, db.ListStackPageInNamespaceRow(got[i]))
		}
	}

	out := make([]codec.StackRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, codec.StackRow{
			Namespace:   row.Namespace,
			Project:     row.Project,
			Name:        row.Name,
			ID:          row.ID,
			Version:     row.Version,
			CreatedAtMs: row.CreatedAtMs,
			UpdatedAtMs: row.UpdatedAtMs,
		})
	}

	return out, nil
}

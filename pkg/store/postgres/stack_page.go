package postgres

import (
	"context"
	"fmt"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
)

// QueryStacks implements stack.Store with one page query in the order of the
// primary key, and one tag query for the stacks of the page.
func (s *Store) QueryStacks(ctx context.Context, q stack.PageQuery) ([]stack.Stack, error) {
	if len(q.Namespaces) == 0 {
		return []stack.Stack{}, nil
	}

	after := stack.PageCursor{Namespace: "", Project: "", Name: ""}
	if q.After != nil {
		after = *q.After
	}

	rows, err := s.queries.ListStackPage(ctx, db.ListStackPageParams{
		Namespaces:     namespaceStrings(q.Namespaces),
		Project:        q.Project.String(),
		AfterNamespace: after.Namespace.String(),
		AfterProject:   after.Project.String(),
		AfterName:      after.Name.String(),
		PageLimit:      int32(q.PageLimit()), //nolint:gosec // PageLimit is at most MaxPageLimit.
	})
	if err != nil {
		return nil, fmt.Errorf("select stack page: %w", err)
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

	return stackPageOf(rows, tagRows), nil
}

// stackPageOf converts the rows of one stack page and their tag rows into
// stacks.
func stackPageOf(rows []db.ListStackPageRow, tagRows []db.StackTag) []stack.Stack {
	tags := make(map[string]map[string]string, len(rows))

	for _, t := range tagRows {
		if tags[t.StackID] == nil {
			tags[t.StackID] = map[string]string{}
		}

		tags[t.StackID][t.Name] = t.Value
	}

	out := make([]stack.Stack, 0, len(rows))

	for _, row := range rows {
		out = append(out, codec.FromStackRow(codec.StackRow{
			Namespace:   row.Namespace,
			Project:     row.Project,
			Name:        row.Name,
			ID:          row.ID,
			Version:     row.Version,
			CreatedAtMs: row.CreatedAtMs,
			UpdatedAtMs: row.UpdatedAtMs,
		}, tags[row.ID]))
	}

	return out
}

// namespaceStrings returns the names as strings.
func namespaceStrings(names []namespace.Name) []string {
	out := make([]string, len(names))
	for i := range names {
		out[i] = names[i].String()
	}

	return out
}

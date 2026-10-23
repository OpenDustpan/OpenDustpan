package postgres

import (
	"context"
	"fmt"

	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
)

// QueryStacks implements stack.Store. It reads the range of each namespace of
// q in the order of the page, with a range of the primary key, until the page
// is full, then the tags of the stacks of the page in one query. All the
// statements run in one snapshot read (see inSnapshotTx), so the tags belong
// to the same state as the stacks, and a restore swap cannot run between them.
func (s *Store) QueryStacks(ctx context.Context, q stack.PageQuery) ([]stack.Stack, error) {
	var out []stack.Stack

	err := s.inSnapshotTx(ctx, func(qtx *db.Queries) error {
		var readErr error

		out, readErr = queryStackPage(ctx, qtx, q)

		return readErr
	})
	if err != nil {
		return nil, fmt.Errorf("stack page snapshot: %w", err)
	}

	return out, nil
}

// queryStackPage reads the page of q with qtx: the ranges of its namespaces,
// then the tags of the stacks of the page.
func queryStackPage(ctx context.Context, qtx *db.Queries, q stack.PageQuery) ([]stack.Stack, error) {
	limit := q.PageLimit()
	rows := []codec.StackRow{}

	for _, r := range q.Ranges() {
		need := limit - len(rows)
		if need == 0 {
			break
		}

		part, err := stackRange(ctx, qtx, r, q, need)
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

	tagRows, err := qtx.ListStackTagsOfStacks(ctx, ids)
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

// stackRange reads at most need stacks of the range r that pass the project
// and the tag filter of q. A project that is not empty keeps the stacks of
// that project only.
func stackRange(
	ctx context.Context, qtx *db.Queries, r stack.NamespaceRange, q stack.PageQuery, need int,
) ([]codec.StackRow, error) {
	var rows []db.ListStackPageInNamespaceRow

	var tag stack.TagFilter
	if q.Tag != nil {
		tag = *q.Tag
	}

	if q.Project == "" {
		got, err := qtx.ListStackPageInNamespace(ctx, db.ListStackPageInNamespaceParams{
			Namespace:    r.Namespace.String(),
			AfterProject: r.AfterProject.String(),
			AfterName:    r.AfterName.String(),
			TagName:      tag.Name,
			MatchValue:   tag.HasValue,
			TagValue:     tag.Value,
			PageLimit:    int32(need), //nolint:gosec // need is at most MaxPageLimit.
		})
		if err != nil {
			return nil, fmt.Errorf("select stack page: %w", err)
		}

		rows = got
	} else {
		got, err := qtx.ListStackPageInProject(ctx, db.ListStackPageInProjectParams{
			Namespace:  r.Namespace.String(),
			Project:    q.Project.String(),
			AfterName:  r.AfterName.String(),
			TagName:    tag.Name,
			MatchValue: tag.HasValue,
			TagValue:   tag.Value,
			PageLimit:  int32(need), //nolint:gosec // need is at most MaxPageLimit.
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

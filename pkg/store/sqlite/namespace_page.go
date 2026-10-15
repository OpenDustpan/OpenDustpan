package sqlite

import (
	"context"
	"encoding/json"
	"fmt"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/sqlite/internal/db"
)

var _ namespace.Pager = (*Store)(nil)

// PageNamespaces implements namespace.Pager. The filter and the page run
// in SQL: with All set it reads past After, otherwise it keeps only the
// names in the query. The names travel as one JSON array, so the query
// stays fixed.
func (s *Store) PageNamespaces(ctx context.Context, q namespace.PageQuery) ([]namespace.Namespace, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if q.All {
		rows, err := s.queries.PageNamespaces(ctx, db.PageNamespacesParams{
			After: q.After.String(),
			Lim:   int64(q.Limit),
		})
		if err != nil {
			return nil, fmt.Errorf("select namespaces page: %w", err)
		}

		return pageRows(rows), nil
	}

	raw, err := json.Marshal(pageNameStrings(q.Names))
	if err != nil {
		return nil, fmt.Errorf("encode namespace names: %w", err)
	}

	rows, err := s.queries.PageNamespacesByNames(ctx, db.PageNamespacesByNamesParams{
		After: q.After.String(),
		Names: string(raw),
		Lim:   int64(q.Limit),
	})
	if err != nil {
		return nil, fmt.Errorf("select namespaces page: %w", err)
	}

	out := make([]namespace.Namespace, 0, len(rows))
	for _, row := range rows {
		out = append(out, codec.FromNamespaceRow(codec.NamespaceRow(row)))
	}

	return out, nil
}

// CountNamespaces implements namespace.Pager. It counts under the same
// All / Names rule as PageNamespaces.
//
//nolint:revive // The all flag is the port: the page and the count share one rule, so the caller names all or a name set.
func (s *Store) CountNamespaces(ctx context.Context, all bool, names []namespace.Name) (int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if all {
		total, err := s.queries.CountNamespaces(ctx)
		if err != nil {
			return 0, fmt.Errorf("count namespaces: %w", err)
		}

		return total, nil
	}

	raw, err := json.Marshal(pageNameStrings(names))
	if err != nil {
		return 0, fmt.Errorf("encode namespace names: %w", err)
	}

	total, err := s.queries.CountNamespacesByNames(ctx, string(raw))
	if err != nil {
		return 0, fmt.Errorf("count namespaces: %w", err)
	}

	return total, nil
}

// pageRows converts page rows into domain namespaces. The page queries
// return the columns of ListNamespaces, so the shared converter applies.
func pageRows(rows []db.PageNamespacesRow) []namespace.Namespace {
	out := make([]namespace.Namespace, 0, len(rows))
	for _, row := range rows {
		out = append(out, codec.FromNamespaceRow(codec.NamespaceRow(row)))
	}

	return out
}

// pageNameStrings returns the plain strings of names, never nil, so an
// empty set encodes as an empty JSON array and matches nothing.
func pageNameStrings(names []namespace.Name) []string {
	out := make([]string, 0, len(names))
	for _, name := range names {
		out = append(out, name.String())
	}

	return out
}

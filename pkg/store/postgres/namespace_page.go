package postgres

import (
	"context"
	"fmt"
	"math"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
)

var _ namespace.Pager = (*Store)(nil)

// PageNamespaces implements namespace.Pager. The filter and the page run
// in SQL: with All set it reads past After, otherwise it keeps only the
// names in the query. The comparison and the order use the column collation,
// as ListNamespaces does, so the page and the count agree with the list.
func (s *Store) PageNamespaces(ctx context.Context, q namespace.PageQuery) ([]namespace.Namespace, error) {
	if q.All {
		rows, err := s.queries.PageNamespaces(ctx, db.PageNamespacesParams{
			After: q.After.String(),
			Lim:   int32(min(max(q.Limit, 0), math.MaxInt32)),
		})
		if err != nil {
			return nil, fmt.Errorf("select namespaces page: %w", err)
		}

		return pageRows(rows), nil
	}

	rows, err := s.queries.PageNamespacesByNames(ctx, db.PageNamespacesByNamesParams{
		After: q.After.String(),
		Names: pageNameStrings(q.Names),
		Lim:   int32(min(max(q.Limit, 0), math.MaxInt32)),
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
	if all {
		total, err := s.queries.CountNamespaces(ctx)
		if err != nil {
			return 0, fmt.Errorf("count namespaces: %w", err)
		}

		return total, nil
	}

	total, err := s.queries.CountNamespacesByNames(ctx, pageNameStrings(names))
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
// empty set matches nothing.
func pageNameStrings(names []namespace.Name) []string {
	out := make([]string, 0, len(names))
	for _, name := range names {
		out = append(out, name.String())
	}

	return out
}

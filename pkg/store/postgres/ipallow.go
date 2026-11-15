package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/ipallow"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
)

var _ ipallow.Store = (*Store)(nil)

// NamespaceList implements ipallow.Store. It reads the allowlist column of
// the namespace. The zero List means the namespace has no rule.
func (s *Store) NamespaceList(ctx context.Context, ns namespace.Name) (ipallow.List, error) {
	raw, err := s.queries.GetNamespaceIPAllowlist(ctx, ns.String())
	if errors.Is(err, pgx.ErrNoRows) {
		return ipallow.List{}, namespace.ErrNotFound
	}

	if err != nil {
		return ipallow.List{}, fmt.Errorf("select namespace IP allowlist: %w", err)
	}

	l, err := ipallow.DecodeList(raw)
	if err != nil {
		return ipallow.List{}, fmt.Errorf("decode namespace IP allowlist: %w", err)
	}

	return l, nil
}

// NamespacesWithList implements ipallow.Store. It returns the names of the
// namespaces with a list, in name order. It refuses a stored name that is not
// a valid namespace name.
func (s *Store) NamespacesWithList(ctx context.Context) ([]namespace.Name, error) {
	names, err := s.queries.NamespacesWithList(ctx)
	if err != nil {
		return nil, fmt.Errorf("select namespaces with an IP allowlist: %w", err)
	}

	out := make([]namespace.Name, 0, len(names))
	for i, name := range names {
		// Defense in depth: the namespace create path validates each name too;
		// this check stays so that a row from a restored backup or another
		// version of the schema cannot reach the scope as a namespace name.
		parsed, parseErr := namespace.ParseName(name)
		if parseErr != nil {
			return nil, fmt.Errorf("row %d of the namespaces with an IP allowlist: %w", i+1, parseErr)
		}

		out = append(out, parsed)
	}

	return out, nil
}

// SetNamespaceList implements ipallow.Store. It replaces the allowlist column
// of the namespace and appends the audit entry e to the chain of the
// namespace in one fenced transaction (ADR 60, fail closed).
func (s *Store) SetNamespaceList(
	ctx context.Context, ns namespace.Name, l ipallow.List, fence namespace.Fence, e audit.Entry,
) error {
	return s.inFencedAuditedTx(ctx, fence, &e, func(q *db.Queries) error {
		n, err := q.SetNamespaceIPAllowlist(ctx, db.SetNamespaceIPAllowlistParams{
			IpAllowlist: l.Encode(),
			Name:        ns.String(),
		})
		if err != nil {
			return fmt.Errorf("update namespace IP allowlist: %w", err)
		}

		if n == 0 {
			return namespace.ErrNotFound
		}

		return nil
	})
}

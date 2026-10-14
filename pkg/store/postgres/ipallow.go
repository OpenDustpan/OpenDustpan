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

// SetNamespaceList implements ipallow.Store. It replaces the allowlist column
// of the namespace and appends the audit entry e to the chain of the
// namespace in one fenced transaction (ADR 60, fail closed).
func (s *Store) SetNamespaceList(
	ctx context.Context, ns namespace.Name, l ipallow.List, fence namespace.Fence, e audit.Entry,
) error {
	return s.inFencedAuditedTx(ctx, fence, e, func(q *db.Queries) error {
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

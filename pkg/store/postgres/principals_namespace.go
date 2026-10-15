package postgres

import (
	"context"
	"fmt"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
)

// CreateNamespacePrincipal implements principal.Store.
func (s *Store) CreateNamespacePrincipal(
	ctx context.Context, p principal.Principal, fence namespace.Fence, e audit.Entry,
) error {
	if err := principalFence(p.Namespace, fence); err != nil {
		return err
	}

	return s.inFencedAuditedTx(ctx, fence, &e, func(q *db.Queries) error {
		return insertPrincipal(ctx, q, p)
	})
}

// ListNamespacePrincipals implements principal.Store. It reads the page and
// the total in one transaction.
func (s *Store) ListNamespacePrincipals(
	ctx context.Context, ns namespace.Name, pq principal.Query,
) ([]principal.Principal, int64, error) {
	if ns == "" {
		return nil, 0, fmt.Errorf("%w: no namespace", principal.ErrInvalid)
	}

	return listPage(ctx, s, pq, pageReads[db.ListPrincipalsRow, principal.Principal]{
		list: func(ctx context.Context, q *db.Queries, pq principal.Query) ([]db.ListPrincipalsRow, error) {
			return listPrincipalRows(ctx, q, pq, "", ns)
		},
		count: func(ctx context.Context, q *db.Queries, pq principal.Query) (int64, error) {
			return countPrincipalRows(ctx, q, pq, "", ns)
		},
		conv: principalFromList,
	})
}

// UpdateNamespacePrincipal implements principal.Store.
func (s *Store) UpdateNamespacePrincipal(
	ctx context.Context, p principal.Principal, fence namespace.Fence, e audit.Entry,
) error {
	if err := principalFence(p.Namespace, fence); err != nil {
		return err
	}

	return s.inFencedAuditedTx(ctx, fence, &e, func(q *db.Queries) error {
		return updatePrincipalRow(ctx, q, p, p.Namespace)
	})
}

// DeleteNamespacePrincipal implements principal.Store. It ends the
// principal, its grants and the grants on it in the transaction of the
// entry. A principal of a namespace is a member of no group.
func (s *Store) DeleteNamespacePrincipal(
	ctx context.Context, id string, fence namespace.Fence, e audit.Entry,
) error {
	if err := principalFence(fence.Namespace, fence); err != nil {
		return err
	}

	ns := fence.Namespace

	return s.inFencedAuditedTx(
		ctx,
		fence,
		&e,
		endRecordTx(ctx, e, access.Subject{Kind: access.SubjectPrincipal, ID: id}, ending{
			record:  func(q *db.Queries, at int64) (int64, error) { return endPrincipal(ctx, q, id, ns, at) },
			members: func(q *db.Queries, at int64) error { return endPrincipalMemberships(ctx, q, id, at) },
		}),
	)
}

// principalFence returns ErrInvalid when fence does not name the namespace
// ns of a principal of a namespace.
func principalFence(ns namespace.Name, fence namespace.Fence) error {
	if ns == "" || fence.Namespace != ns {
		return fmt.Errorf("%w: the fence does not name the namespace of the principal", principal.ErrInvalid)
	}

	return nil
}

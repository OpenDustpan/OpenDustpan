package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/savedview"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/internal/viewstore"
	"opendustpan.org/opendustpan/pkg/store/sqlite/internal/db"
)

// SavedViews returns the store of saved views. Each write checks the
// installation state and the fence of its namespace, and appends its audit
// entry in its own transaction.
//
//nolint:ireturn // Store-agnostic wiring takes the port.
func (s *Store) SavedViews() savedview.Store {
	return savedViews{s: s}
}

// savedViews is savedview.Store over the database of a Store.
type savedViews struct {
	s *Store
}

var _ savedview.Store = savedViews{s: nil}

// Create implements savedview.Store. When ctx carries an idempotency
// completion, the transaction also stores its answer for the view.
func (v savedViews) Create(ctx context.Context, view savedview.View, fence namespace.Fence, e audit.Entry) error {
	if err := view.Validate(); err != nil {
		return fmt.Errorf("create saved view: %w", err)
	}

	// Defense in depth: the fence check below compares the namespace of the
	// fence with the namespace row. This check stays so that a view of one
	// namespace cannot be written under the fence of another namespace, whatever
	// the audit entry says. It does not rely on the service.
	if view.Namespace != fence.Namespace {
		return fmt.Errorf("%w: view names %q, fence names %q", namespace.ErrFenced, view.Namespace, fence.Namespace)
	}

	query, err := viewstore.EncodeQuery(view.Query)
	if err != nil {
		return err
	}

	v.s.mu.RLock()
	defer v.s.mu.RUnlock()

	return v.s.inFencedAuditedTx(ctx, fence, &e, func(q *db.Queries) error {
		n, insErr := q.InsertSavedView(ctx, db.InsertSavedViewParams{
			ID: string(view.ID), Namespace: view.Namespace.String(), Owner: view.Owner, Name: view.Name,
			QueryJson: query, CreatedAtMs: codec.Millis(view.CreatedAt),
		})
		if insErr != nil {
			return fmt.Errorf("insert saved view: %w", insErr)
		}

		if n == 0 {
			return savedview.ErrExists
		}

		// The write lock of the transaction is held, so the counts below are
		// exact: no other create runs between the insert and the commit.
		inNamespace, countErr := q.CountSavedViewsInNamespace(ctx, view.Namespace.String())
		if countErr != nil {
			return fmt.Errorf("count saved views of the namespace: %w", countErr)
		}

		ofOwner, countErr := q.CountSavedViewsOfOwner(ctx, db.CountSavedViewsOfOwnerParams{
			Namespace: view.Namespace.String(), Owner: view.Owner,
		})
		if countErr != nil {
			return fmt.Errorf("count saved views of the owner: %w", countErr)
		}

		if inNamespace > savedview.MaxPerNamespace || ofOwner > savedview.MaxPerOwner {
			return savedview.ErrLimit
		}

		return finishCompletion(ctx, q, view)
	})
}

// Get implements savedview.Store.
func (v savedViews) Get(ctx context.Context, ns namespace.Name, id savedview.ID, owner string) (savedview.View, error) {
	v.s.mu.RLock()
	defer v.s.mu.RUnlock()

	row, err := v.s.queries.GetSavedView(ctx, db.GetSavedViewParams{Namespace: ns.String(), ID: string(id), Owner: owner})
	if errors.Is(err, sql.ErrNoRows) {
		return savedview.View{}, savedview.ErrNotFound
	}

	if err != nil {
		return savedview.View{}, fmt.Errorf("select saved view: %w", err)
	}

	out, err := viewstore.View(row.ID, row.Namespace, row.Owner, row.Name, row.QueryJson, row.CreatedAtMs)
	if err != nil {
		// A row that fails its checks is not served, and it reads as no view.
		return savedview.View{}, savedview.ErrNotFound
	}

	// Defense in depth: the query matched the owner. This check stays so that a
	// change of the query cannot serve a view to another owner.
	if out.Owner != owner {
		return savedview.View{}, savedview.ErrNotFound
	}

	return out, nil
}

// List implements savedview.Store. A row that fails its checks is left out.
func (v savedViews) List(ctx context.Context, ns namespace.Name, owner string) ([]savedview.View, error) {
	v.s.mu.RLock()
	defer v.s.mu.RUnlock()

	rows, err := v.s.queries.ListSavedViewsOfOwner(ctx, db.ListSavedViewsOfOwnerParams{Namespace: ns.String(), Owner: owner})
	if err != nil {
		return nil, fmt.Errorf("select saved views: %w", err)
	}

	out := make([]savedview.View, 0, len(rows))

	for _, row := range rows {
		view, viewErr := viewstore.View(row.ID, row.Namespace, row.Owner, row.Name, row.QueryJson, row.CreatedAtMs)
		if viewErr != nil || view.Owner != owner {
			continue
		}

		out = append(out, view)
	}

	return out, nil
}

// Delete implements savedview.Store. When ctx carries an idempotency
// completion, the transaction also stores its answer.
func (v savedViews) Delete(
	ctx context.Context, ns namespace.Name, id savedview.ID, owner string, fence namespace.Fence, e audit.Entry,
) error {
	if ns != fence.Namespace {
		return fmt.Errorf("%w: view names %q, fence names %q", namespace.ErrFenced, ns, fence.Namespace)
	}

	v.s.mu.RLock()
	defer v.s.mu.RUnlock()

	return v.s.inFencedAuditedTx(ctx, fence, &e, func(q *db.Queries) error {
		n, err := q.DeleteSavedView(ctx, db.DeleteSavedViewParams{Namespace: ns.String(), ID: string(id), Owner: owner})
		if err != nil {
			return fmt.Errorf("delete saved view: %w", err)
		}

		if n == 0 {
			return savedview.ErrNotFound
		}

		// When ctx carries an idempotency completion, the answer of the delete
		// is stored in this transaction.
		return finishCompletion(ctx, q, id)
	})
}

// DeleteOwned implements savedview.Store.
func (v savedViews) DeleteOwned(
	ctx context.Context, ns namespace.Name, owner string, fence namespace.Fence, e audit.Entry,
) (int, error) {
	// Defense in depth: the query matches the owner exactly. This check stays
	// so that an empty owner or a pattern can never select the views of many
	// owners, whatever a later change does to the query.
	if !savedview.ValidOwner(owner) {
		return 0, fmt.Errorf("%w: the owner is not a principal ID", savedview.ErrInvalid)
	}

	if err := e.Validate(); err != nil {
		return 0, fmt.Errorf("audit entry: %w", err)
	}

	if ns != fence.Namespace || e.Namespace != ns {
		return 0, fmt.Errorf("%w: view names %q, fence names %q", namespace.ErrFenced, ns, fence.Namespace)
	}

	v.s.mu.RLock()
	defer v.s.mu.RUnlock()

	var removed int

	err := v.s.inFencedTx(ctx, fence, func(q *db.Queries) error {
		ids, err := q.ListSavedViewIDsOfOwner(ctx, db.ListSavedViewIDsOfOwnerParams{Namespace: ns.String(), Owner: owner})
		if err != nil {
			return fmt.Errorf("select saved view IDs: %w", err)
		}

		for _, id := range ids {
			n, delErr := q.DeleteSavedView(ctx, db.DeleteSavedViewParams{Namespace: ns.String(), ID: id, Owner: owner})
			if delErr != nil {
				return fmt.Errorf("delete saved view: %w", delErr)
			}

			if n == 0 {
				continue
			}

			entry := e
			entry.ObjectID = id

			if _, appendErr := appendAuditEntry(ctx, q, entry); appendErr != nil {
				return fmt.Errorf("append audit entry: %w", appendErr)
			}

			removed++
		}

		return nil
	})
	if err != nil {
		return 0, err
	}

	return removed, nil
}

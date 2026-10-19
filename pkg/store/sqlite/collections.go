package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/sqlite/internal/db"
)

var _ collection.Store = (*Store)(nil)

// CreateCollection implements collection.Store. The fence is checked and the
// audit entry is appended in the same transaction as the insert (ADR 4, ADR 60).
func (s *Store) CreateCollection(
	ctx context.Context, c collection.Collection, fence namespace.Fence, e audit.Entry,
) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.inFencedAuditedTx(ctx, fence, &e, func(q *db.Queries) error {
		n, err := q.CreateCollection(ctx, db.CreateCollectionParams{
			ID:          c.ID,
			Namespace:   c.Ref.Namespace.String(),
			NameKey:     c.Ref.Name.Key(),
			Name:        c.Ref.Name.String(),
			CreatedAtMs: codec.Millis(c.CreatedAt),
		})
		if err != nil {
			return fmt.Errorf("insert collection: %w", err)
		}

		if n == 0 {
			return collection.ErrAlreadyExists
		}

		return nil
	})
}

// GetCollection implements collection.Store.
func (s *Store) GetCollection(ctx context.Context, ref collection.Ref) (collection.Collection, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	row, err := s.queries.GetCollection(ctx, db.GetCollectionParams{
		Namespace: ref.Namespace.String(),
		NameKey:   ref.Name.Key(),
	})
	if errors.Is(err, sql.ErrNoRows) {
		return collection.Collection{}, collection.ErrNotFound
	}

	if err != nil {
		return collection.Collection{}, fmt.Errorf("select collection: %w", err)
	}

	return collectionFromRow(row.ID, row.Namespace, row.Name, row.CreatedAtMs), nil
}

// ListCollections implements collection.Store. A namespace with no collection
// reads as an empty list.
func (s *Store) ListCollections(ctx context.Context, ns namespace.Name) ([]collection.Collection, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.queries.ListCollections(ctx, ns.String())
	if err != nil {
		return nil, fmt.Errorf("select collections: %w", err)
	}

	out := make([]collection.Collection, 0, len(rows))
	for _, row := range rows {
		out = append(out, collectionFromRow(row.ID, row.Namespace, row.Name, row.CreatedAtMs))
	}

	return out, nil
}

// DeleteCollection implements collection.Store. A collection with a live unit is
// kept. The memberships of its soft-deleted units are removed in the same
// transaction, so a deleted unit does not keep the collection.
func (s *Store) DeleteCollection(
	ctx context.Context, ref collection.Ref, fence namespace.Fence, e audit.Entry,
) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.inFencedAuditedTx(ctx, fence, &e, func(q *db.Queries) error {
		live, err := q.CountLiveCollectionMembers(ctx, db.CountLiveCollectionMembersParams{
			Namespace:     ref.Namespace.String(),
			CollectionKey: ref.Name.Key(),
		})
		if err != nil {
			return fmt.Errorf("count collection units: %w", err)
		}

		if live > 0 {
			return collection.ErrNotEmpty
		}

		id, idErr := collectionID(ctx, q, ref)
		if idErr != nil {
			return idErr
		}

		if revokeErr := s.revokeCollectionGrants(ctx, q, id, e); revokeErr != nil {
			return revokeErr
		}

		if gateErr := purgeCollectionGate(ctx, q, ref); gateErr != nil {
			return gateErr
		}

		return removeCollection(ctx, q, ref)
	})
}

// SetUnitCollection implements collection.Store. The unit is resolved through
// the inventory, so the membership is keyed by the unit ID and survives a rename.
// The expected source collection is compared inside the transaction.
func (s *Store) SetUnitCollection(
	ctx context.Context, m collection.Move, fence namespace.Fence, e audit.Entry,
) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.inFencedAuditedTx(ctx, fence, &e, func(q *db.Queries) error {
		return setUnitCollection(ctx, q, m, e)
	})
}

// setUnitCollection moves the unit into the collection to, in the caller's
// transaction. It returns ErrCollectionChanged when the unit is no longer in
// from, so a move made on a stale read is refused.
func setUnitCollection(ctx context.Context, q *db.Queries, m collection.Move, e audit.Entry) error {
	unit, from, to := m.Unit, m.From, m.To

	if unit.Namespace != to.Namespace {
		return collection.ErrNotFound
	}

	unitID, err := q.GetInventoryUnit(ctx, db.GetInventoryUnitParams{
		Namespace: unit.Namespace.String(),
		Tool:      unit.Tool,
		Name:      unit.Name,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return collection.ErrUnitNotFound
	}

	if err != nil {
		return fmt.Errorf("select inventory unit: %w", err)
	}

	target, err := q.GetCollection(ctx, db.GetCollectionParams{
		Namespace: to.Namespace.String(),
		NameKey:   to.Name.Key(),
	})
	if errors.Is(err, sql.ErrNoRows) {
		return collection.ErrNotFound
	}

	if err != nil {
		return fmt.Errorf("select target collection: %w", err)
	}

	if target.ID != m.ToID {
		return collection.ErrCollectionChanged
	}

	current, err := currentUnitCollection(ctx, q, unitID)
	if err != nil {
		return err
	}

	if current.Key() != from.Key() {
		return collection.ErrCollectionChanged
	}

	err = q.UpsertUnitCollection(ctx, db.UpsertUnitCollectionParams{
		UnitID:        unitID,
		Namespace:     unit.Namespace.String(),
		CollectionKey: to.Name.Key(),
		AssignedAtMs:  codec.Millis(e.At),
	})
	if err != nil {
		return fmt.Errorf("upsert unit collection: %w", err)
	}

	return nil
}

// UnitCollection implements collection.Store.
func (s *Store) UnitCollection(ctx context.Context, unit collection.UnitRef) (collection.Ref, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	unitID, err := s.queries.GetInventoryUnit(ctx, db.GetInventoryUnitParams{
		Namespace: unit.Namespace.String(),
		Tool:      unit.Tool,
		Name:      unit.Name,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return collection.Ref{}, collection.ErrUnitNotFound
	}

	if err != nil {
		return collection.Ref{}, fmt.Errorf("select inventory unit: %w", err)
	}

	name, err := currentUnitCollection(ctx, s.queries, unitID)
	if err != nil {
		return collection.Ref{}, err
	}

	if name == "" {
		return collection.Ref{}, collection.ErrNoCollection
	}

	return collection.Ref{Namespace: unit.Namespace, Name: name}, nil
}

// currentUnitCollection returns the collection that the unit with unitID is in,
// or the empty name when it has none.
func currentUnitCollection(ctx context.Context, q *db.Queries, unitID string) (collection.Name, error) {
	name, err := q.GetUnitCollection(ctx, unitID)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return "", nil
	case err != nil:
		return "", fmt.Errorf("select unit collection: %w", err)
	default:
		return collection.Name(name), nil
	}
}

// collectionFromRow converts a collections row into a domain collection.
func collectionFromRow(id, ns, name string, createdAtMs int64) collection.Collection {
	return collection.Collection{
		CreatedAt: codec.FromMillis(createdAtMs),
		ID:        id,
		Ref: collection.Ref{
			Namespace: namespace.Name(ns),
			Name:      collection.Name(name),
		},
	}
}

// revokeCollectionGrants ends every current grant on the collection with the
// stable ID id. Each grant gets its own audit entry, in the same transaction
// as the delete, so a deleted collection leaves no grant that a re-created
// name could inherit.
func (s *Store) revokeCollectionGrants(ctx context.Context, q *db.Queries, id string, e audit.Entry) error {
	ids, err := q.GrantIDsOnTarget(ctx, collectionTargetPrefix+id)
	if err != nil {
		return fmt.Errorf("list collection grants: %w", err)
	}

	for _, gid := range ids {
		ge := e
		ge.Object = "grant:" + gid
		ge.ObjectID = gid

		if _, err := appendAuditEntry(ctx, q, ge); err != nil {
			return fmt.Errorf("audit revoke of grant %s: %w", gid, err)
		}

		n, err := q.RevokeGrant(ctx, db.RevokeGrantParams{
			RevokedAtMs: nullMillis(codec.Millis(e.At)), RevokedBy: e.Subject, ID: gid,
		})
		if err != nil {
			return fmt.Errorf("revoke grant %s: %w", gid, err)
		}

		if n == 0 {
			return fmt.Errorf("revoke grant %s: %w", gid, access.ErrNotFound)
		}
	}

	return nil
}

// collectionID returns the stable ID of the collection ref in the transaction
// q. It returns collection.ErrNotFound when no such collection exists.
func collectionID(ctx context.Context, q *db.Queries, ref collection.Ref) (string, error) {
	row, err := q.GetCollection(ctx, db.GetCollectionParams{
		Namespace: ref.Namespace.String(),
		NameKey:   ref.Name.Key(),
	})
	if errors.Is(err, sql.ErrNoRows) {
		return "", collection.ErrNotFound
	}

	if err != nil {
		return "", fmt.Errorf("select collection: %w", err)
	}

	return row.ID, nil
}

// removeCollection deletes the memberships and then the row of the collection
// ref, in the transaction q. The caller has checked that no live unit is in
// it.
func removeCollection(ctx context.Context, q *db.Queries, ref collection.Ref) error {
	err := q.DeleteCollectionMembers(ctx, db.DeleteCollectionMembersParams{
		Namespace:     ref.Namespace.String(),
		CollectionKey: ref.Name.Key(),
	})
	if err != nil {
		return fmt.Errorf("delete collection memberships: %w", err)
	}

	n, err := q.DeleteCollection(ctx, db.DeleteCollectionParams{
		Namespace: ref.Namespace.String(),
		NameKey:   ref.Name.Key(),
	})
	if err != nil {
		return fmt.Errorf("delete collection: %w", err)
	}

	if n == 0 {
		return collection.ErrNotFound
	}

	return nil
}

package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/sqlite/internal/db"
)

var _ secret.ItemKeyStore = (*Store)(nil)

// itemKeyTx is the item key part of one open fenced transaction of the store.
// Its fence is the fence that opened the transaction.
type itemKeyTx struct {
	q     *db.Queries
	fence namespace.Fence
}

var _ secret.ItemKeyTx = itemKeyTx{q: nil, fence: namespace.Fence{Namespace: "", FailoverVersion: 0}}

// InItemKeyTx implements secret.ItemKeyStore. The transaction checks fence
// first, and fn writes the item row and the record in it.
func (s *Store) InItemKeyTx(ctx context.Context, fence namespace.Fence, fn func(secret.ItemKeyTx) error) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.inFencedTx(ctx, fence, func(q *db.Queries) error {
		return fn(itemKeyTx{q: q, fence: fence})
	})
}

// CreateItemKey implements secret.ItemKeyTx. It stores k unless a record for
// its item exists, and returns the stored record either way, so two writers of
// the first save of one item agree on one key.
func (t itemKeyTx) CreateItemKey(ctx context.Context, k secret.ItemKey) (secret.ItemKey, error) {
	// Defense in depth: the transaction checked the failover version of the
	// namespace of the fence only. This check stays so that a record of one
	// namespace never lands under the fence of another namespace.
	if err := codec.CheckNewItemKey(k, t.fence); err != nil {
		return secret.ItemKey{}, err
	}

	row := codec.ToItemKeyRow(k)

	_, err := t.q.InsertItemKey(ctx, db.InsertItemKeyParams{
		ID: row.ID, Namespace: row.Namespace, ScopeID: row.ScopeID, Kind: row.Kind, Item: row.Item,
		WrapperID: row.WrapperID, CreatedAtMs: row.CreatedAtMs, BlobVersion: row.BlobVersion,
		BlobSize: row.BlobSize, BlobMd5: row.BlobMd5,
	})
	if err != nil {
		return secret.ItemKey{}, fmt.Errorf("insert item key: %w", err)
	}

	return t.GetItemKey(ctx, k.Ref)
}

// GetNamespace implements secret.ItemKeyTx.
func (t itemKeyTx) GetNamespace(ctx context.Context, name namespace.Name) (namespace.Namespace, error) {
	row, err := t.q.GetNamespace(ctx, name.String())
	if errors.Is(err, sql.ErrNoRows) {
		return namespace.Namespace{}, namespace.ErrNotFound
	}

	if err != nil {
		return namespace.Namespace{}, fmt.Errorf("select namespace: %w", err)
	}

	return codec.FromNamespaceRow(codec.NamespaceRow(row)), nil
}

// GetItemKey implements secret.ItemKeyTx.
func (t itemKeyTx) GetItemKey(ctx context.Context, ref secret.ItemRef) (secret.ItemKey, error) {
	return shreddedAsError(itemKeyOf(ctx, t.q, ref))
}

// shreddedAsError turns a shredded record into secret.ErrShredded, with the
// record.
func shreddedAsError(got secret.ItemKey, err error) (secret.ItemKey, error) {
	if err != nil {
		return secret.ItemKey{}, err
	}

	if got.Shredded() {
		return got, fmt.Errorf("item key of %s: %w", got.Ref, secret.ErrShredded)
	}

	return got, nil
}

// CreateItemKey implements secret.ItemKeyIndex in a transaction of its own.
// A caller that creates the item too uses InItemKeyTx and writes both in one
// transaction.
func (s *Store) CreateItemKey(ctx context.Context, k secret.ItemKey, fence namespace.Fence) (secret.ItemKey, error) {
	var stored secret.ItemKey

	err := s.InItemKeyTx(ctx, fence, func(tx secret.ItemKeyTx) error {
		got, err := tx.CreateItemKey(ctx, k)
		stored = got

		return err
	})
	if err != nil {
		if errors.Is(err, secret.ErrShredded) {
			return stored, err
		}

		return secret.ItemKey{}, err
	}

	return stored, nil
}

// GetItemKey implements secret.ItemKeyIndex.
func (s *Store) GetItemKey(ctx context.Context, ref secret.ItemRef) (secret.ItemKey, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return itemKeyTx{q: s.queries, fence: namespace.Fence{Namespace: "", FailoverVersion: 0}}.GetItemKey(ctx, ref)
}

// itemKeyOf reads the record of ref, shredded or not. It returns
// secret.ErrKeyNotFound for no record.
func itemKeyOf(ctx context.Context, q *db.Queries, ref secret.ItemRef) (secret.ItemKey, error) {
	row, err := q.GetItemKey(ctx, db.GetItemKeyParams{
		Namespace: ref.Scope.Namespace.String(), ScopeID: ref.Scope.ID, Kind: string(ref.Kind), Item: ref.Item,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return secret.ItemKey{}, fmt.Errorf("item key of %s: %w", ref, secret.ErrKeyNotFound)
	}

	if err != nil {
		return secret.ItemKey{}, fmt.Errorf("select item key: %w", err)
	}

	return codec.FromItemKeyRow(codec.ItemKeyRow(row))
}

// ShredItemKey implements secret.ItemKeyIndex. Only a record that is not
// shredded changes, so a second shred keeps the first shred time.
func (s *Store) ShredItemKey(
	ctx context.Context, ref secret.ItemRef, at time.Time, fence namespace.Fence,
) (secret.ItemKey, error) {
	if err := codec.CheckShredItemKey(ref, at, fence); err != nil {
		return secret.ItemKey{}, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	var stored secret.ItemKey

	err := s.inFencedTx(ctx, fence, func(q *db.Queries) error {
		_, err := q.ShredItemKey(ctx, db.ShredItemKeyParams{
			ShreddedAtMs: codec.Millis(at), Namespace: ref.Scope.Namespace.String(), ScopeID: ref.Scope.ID,
			Kind: string(ref.Kind), Item: ref.Item,
		})
		if err != nil {
			return fmt.Errorf("shred item key: %w", err)
		}

		got, err := itemKeyOf(ctx, q, ref)
		if err != nil {
			return err
		}

		stored = got

		return nil
	})
	if err != nil {
		return secret.ItemKey{}, err
	}

	return stored, nil
}

// ListItemKeys implements secret.ItemKeyIndex.
func (s *Store) ListItemKeys(
	ctx context.Context, scope secret.Scope, after secret.ItemKeyID, limit int,
) ([]secret.ItemKey, error) {
	if err := codec.CheckListItemKeys(scope, limit); err != nil {
		return nil, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.queries.ListItemKeys(ctx, db.ListItemKeysParams{
		Namespace: scope.Namespace.String(), ScopeID: scope.ID, After: string(after), MaxRows: int64(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("select item keys: %w", err)
	}

	out := make([]secret.ItemKey, 0, len(rows))

	for i := range rows {
		k, convErr := codec.FromItemKeyRow(codec.ItemKeyRow(rows[i]))
		if convErr != nil {
			return nil, convErr
		}

		out = append(out, k)
	}

	return out, nil
}

// ListItemKeyWrapperIDs implements secret.ItemKeyIndex.
func (s *Store) ListItemKeyWrapperIDs(ctx context.Context) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ids, err := s.queries.ListItemKeyWrapperIDs(ctx)
	if err != nil {
		return nil, fmt.Errorf("select item key wrapper IDs: %w", err)
	}

	if ids == nil {
		return []string{}, nil
	}

	return ids, nil
}

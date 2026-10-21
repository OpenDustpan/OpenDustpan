package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
)

var _ secret.ItemKeyIndex = (*Store)(nil)

// CreateItemKey implements secret.ItemKeyIndex with a fenced insert. It
// stores k unless a record for its item exists, and returns the stored
// record either way, so two writers of the first save of one item agree on
// one key.
func (s *Store) CreateItemKey(ctx context.Context, k secret.ItemKey, fence namespace.Fence) (secret.ItemKey, error) {
	if err := codec.CheckNewItemKey(k, fence); err != nil {
		return secret.ItemKey{}, err
	}

	row := codec.ToItemKeyRow(k)

	var stored secret.ItemKey

	err := s.inFencedTx(ctx, fence, func(q *db.Queries) error {
		_, err := q.InsertItemKey(ctx, db.InsertItemKeyParams{
			ID: row.ID, Namespace: row.Namespace, ScopeID: row.ScopeID, Kind: row.Kind, Item: row.Item,
			WrapperID: row.WrapperID, CreatedAtMs: row.CreatedAtMs,
		})
		if err != nil {
			return fmt.Errorf("insert item key: %w", err)
		}

		got, err := s.itemKeyOf(ctx, q, k.Ref)
		if err != nil {
			return err
		}

		stored = got

		return nil
	})
	if err != nil {
		return secret.ItemKey{}, err
	}

	if stored.Shredded() {
		return stored, fmt.Errorf("create item key %s: %w", k.Ref, secret.ErrShredded)
	}

	return stored, nil
}

// GetItemKey implements secret.ItemKeyIndex.
func (s *Store) GetItemKey(ctx context.Context, ref secret.ItemRef) (secret.ItemKey, error) {
	got, err := s.itemKeyOf(ctx, s.queries, ref)
	if err != nil {
		return secret.ItemKey{}, err
	}

	if got.Shredded() {
		return got, fmt.Errorf("item key of %s: %w", ref, secret.ErrShredded)
	}

	return got, nil
}

// itemKeyOf reads the record of ref, shredded or not. It returns
// secret.ErrKeyNotFound for no record.
func (s *Store) itemKeyOf(ctx context.Context, q *db.Queries, ref secret.ItemRef) (secret.ItemKey, error) {
	row, err := q.GetItemKey(ctx, db.GetItemKeyParams{
		Namespace: ref.Scope.Namespace.String(), ScopeID: ref.Scope.ID, Kind: string(ref.Kind), Item: ref.Item,
	})
	if errors.Is(err, pgx.ErrNoRows) {
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

	var stored secret.ItemKey

	err := s.inFencedTx(ctx, fence, func(q *db.Queries) error {
		_, err := q.ShredItemKey(ctx, db.ShredItemKeyParams{
			ShreddedAtMs: codec.Millis(at), Namespace: ref.Scope.Namespace.String(), ScopeID: ref.Scope.ID,
			Kind: string(ref.Kind), Item: ref.Item,
		})
		if err != nil {
			return fmt.Errorf("shred item key: %w", err)
		}

		got, err := s.itemKeyOf(ctx, q, ref)
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

	rows, err := s.queries.ListItemKeys(ctx, db.ListItemKeysParams{
		Namespace: scope.Namespace.String(), ScopeID: scope.ID, After: string(after), MaxRows: int64(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("select item keys: %w", err)
	}

	out := make([]secret.ItemKey, 0, len(rows))

	for _, row := range rows {
		k, convErr := codec.FromItemKeyRow(codec.ItemKeyRow(row))
		if convErr != nil {
			return nil, convErr
		}

		out = append(out, k)
	}

	return out, nil
}

// ListItemKeyWrapperIDs implements secret.ItemKeyIndex.
func (s *Store) ListItemKeyWrapperIDs(ctx context.Context) ([]string, error) {
	ids, err := s.queries.ListItemKeyWrapperIDs(ctx)
	if err != nil {
		return nil, fmt.Errorf("select item key wrapper IDs: %w", err)
	}

	if ids == nil {
		return []string{}, nil
	}

	return ids, nil
}

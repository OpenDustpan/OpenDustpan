package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/sqlite/internal/db"
)

var _ secret.KeyStore = (*Store)(nil)

// CreateKey implements secret.KeyStore with a fenced insert. It stores k
// unless a key for its scope exists, and returns the stored key either way,
// so concurrent first encryptions agree on one key.
func (s *Store) CreateKey(
	ctx context.Context, k secret.WrappedKey, fence namespace.Fence,
) (secret.WrappedKey, error) {
	row := codec.ToSecretKeyRow(k)

	var stored secret.WrappedKey

	err := s.inFencedTx(ctx, fence, func(q *db.Queries) error {
		_, insertErr := q.CreateSecretKey(ctx, db.CreateSecretKeyParams{
			Namespace:   row.Namespace,
			StackID:     row.StackID,
			WrapperID:   row.WrapperID,
			Ciphertext:  row.Ciphertext,
			CreatedAtMs: row.CreatedAtMs,
		})
		if insertErr != nil {
			return fmt.Errorf("insert secret key: %w", insertErr)
		}

		got, err := q.GetSecretKey(ctx, db.GetSecretKeyParams{
			Namespace: row.Namespace,
			StackID:   row.StackID,
		})
		if err != nil {
			return fmt.Errorf("select stored secret key: %w", err)
		}

		stored = codec.FromSecretKeyRow(codec.SecretKeyRow{
			Namespace:   got.Namespace,
			StackID:     got.StackID,
			WrapperID:   got.WrapperID,
			Ciphertext:  got.Ciphertext,
			CreatedAtMs: got.CreatedAtMs,
		})

		return nil
	})
	if err != nil {
		return secret.WrappedKey{}, err
	}

	return stored, nil
}

// GetKey implements secret.KeyStore.
func (s *Store) GetKey(ctx context.Context, scope secret.Scope) (secret.WrappedKey, error) {
	row, err := s.queries.GetSecretKey(ctx, db.GetSecretKeyParams{
		Namespace: scope.Namespace.String(),
		StackID:   scope.ID,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return secret.WrappedKey{}, secret.ErrKeyNotFound
	}

	if err != nil {
		return secret.WrappedKey{}, fmt.Errorf("select secret key: %w", err)
	}

	return codec.FromSecretKeyRow(codec.SecretKeyRow{
		Namespace:   row.Namespace,
		StackID:     row.StackID,
		WrapperID:   row.WrapperID,
		Ciphertext:  row.Ciphertext,
		CreatedAtMs: row.CreatedAtMs,
	}), nil
}

// ListWrapperIDs implements secret.KeyStore.
func (s *Store) ListWrapperIDs(ctx context.Context) ([]string, error) {
	ids, err := s.queries.ListSecretWrapperIDs(ctx)
	if err != nil {
		return nil, fmt.Errorf("select wrapper IDs: %w", err)
	}

	if ids == nil {
		return []string{}, nil
	}

	return ids, nil
}

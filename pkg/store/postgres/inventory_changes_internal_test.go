package postgres

// Test hooks for the inventory change log suite: the fixture methods that
// inventorytest.Fixture names. They are not port methods. This file compiles
// into the test binary only.

import (
	"context"
	"fmt"

	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
)

// AppendChange runs the append code of the store in a fenced transaction of
// its own and returns the stored row.
func (s *Store) AppendChange(
	ctx context.Context, c inventory.Change, fence namespace.Fence,
) (inventory.Change, error) {
	c.Namespace = fence.Namespace

	err := s.inFencedTx(ctx, fence, func(q *db.Queries) error {
		seq, err := insertInventoryChange(ctx, q, c)
		if err != nil {
			return err
		}

		c.Seq = seq

		return nil
	})
	if err != nil {
		return inventory.Change{}, err
	}

	return c, nil
}

// PurgeNamespace deletes the namespace row of ns and keeps its change log.
func (s *Store) PurgeNamespace(ctx context.Context, ns namespace.Name) error {
	if _, err := s.pool.Exec(ctx, `DELETE FROM namespaces WHERE name = $1`, ns.String()); err != nil {
		return fmt.Errorf("delete namespace: %w", err)
	}

	return nil
}

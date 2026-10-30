package sqlite

import (
	"context"
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/installation"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/schema"
	"opendustpan.org/opendustpan/pkg/store/sqlite/internal/db"
)

var _ installation.InstanceStore = (*Store)(nil)

// RegisterInstance implements installation.InstanceStore. The row is a
// control write: it must work in maintenance mode too, so it takes no state
// check.
func (s *Store) RegisterInstance(ctx context.Context, inst installation.Instance) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Defense in depth: the caller validates the row too; this check stays so
	// that a new caller cannot write a row that the table checks would refuse.
	if err := inst.Validate(); err != nil {
		return fmt.Errorf("register instance: %w", err)
	}

	err := s.inMinimumTx(ctx, func(q *db.Queries) error {
		return q.UpsertInstanceVersion(ctx, db.UpsertInstanceVersionParams{
			ID:            inst.ID,
			BinaryVersion: inst.BinaryVersion,
			StartedAtMs:   codec.Millis(inst.StartedAt),
		})
	})
	if errors.Is(err, schema.ErrBinaryBelowMinimum) {
		return fmt.Errorf("register instance %s: %w", inst.ID, err)
	}

	if err != nil {
		return fmt.Errorf("write instance version sqlite database: %w", installation.ErrUnavailable)
	}

	return nil
}

// UnregisterInstance implements installation.InstanceStore.
func (s *Store) UnregisterInstance(ctx context.Context, id string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Defense in depth: the caller validates the ID too.
	if err := installation.ValidInstanceID(id); err != nil {
		return fmt.Errorf("unregister instance: %w", err)
	}

	err := s.inControlTx(ctx, func(q *db.Queries) error {
		_, execErr := q.DeleteInstanceVersion(ctx, id)

		return execErr
	})
	if err != nil {
		return fmt.Errorf("delete instance version sqlite database: %w", installation.ErrUnavailable)
	}

	return nil
}

// ListInstances implements installation.InstanceStore.
func (s *Store) ListInstances(ctx context.Context) ([]installation.Instance, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.queries.ListInstanceVersions(ctx)
	if err != nil {
		return nil, fmt.Errorf("select instance versions sqlite database: %w", installation.ErrUnavailable)
	}

	out := make([]installation.Instance, 0, len(rows))
	for _, row := range rows {
		out = append(out, installation.Instance{
			StartedAt:     time.UnixMilli(row.StartedAtMs).UTC(),
			ID:            row.ID,
			BinaryVersion: row.BinaryVersion,
		})
	}

	return out, nil
}

// DeleteInstance implements installation.InstanceStore.
func (s *Store) DeleteInstance(ctx context.Context, id string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Defense in depth: the handler validates the ID too.
	if err := installation.ValidInstanceID(id); err != nil {
		return fmt.Errorf("delete instance: %w", err)
	}

	var deleted int64

	err := s.inControlTx(ctx, func(q *db.Queries) error {
		var execErr error

		deleted, execErr = q.DeleteInstanceVersion(ctx, id)

		return execErr
	})
	if err != nil {
		return fmt.Errorf("delete instance version sqlite database: %w", installation.ErrUnavailable)
	}

	if deleted == 0 {
		return fmt.Errorf("delete instance %s: %w", id, installation.ErrInstanceNotFound)
	}

	return nil
}

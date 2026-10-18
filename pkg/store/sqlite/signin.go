package sqlite

import (
	"context"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/internal/authgate"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/sqlite/internal/db"
)

var _ authgate.FailureCounter = (*Store)(nil)

// AddFailure implements authgate.FailureCounter. It reads the window from the
// clock of the store in the same transaction as the write, so the clock of an
// instance never moves the window. The upsert is one atomic write, so
// concurrent adds each count once.
func (s *Store) AddFailure(
	ctx context.Context, source string, length time.Duration,
) (authgate.FailureCount, error) {
	if length < time.Millisecond {
		return authgate.FailureCount{}, authgate.ErrInvalidWindow
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	var out authgate.FailureCount

	err := s.inTx(ctx, func(q *db.Queries) error {
		now, err := q.DatabaseNowMs(ctx)
		if err != nil {
			return fmt.Errorf("read the clock of the store: %w", err)
		}

		lengthMs := length.Milliseconds()
		end := (now/lengthMs + 1) * lengthMs

		count, err := q.AddSignInFailure(ctx, db.AddSignInFailureParams{Source: source, WindowEndMs: end})
		if err != nil {
			return fmt.Errorf("add sign-in failure: %w", err)
		}

		out = authgate.FailureCount{At: codec.FromMillis(now), End: codec.FromMillis(end), Count: count}

		return nil
	})
	if err != nil {
		return authgate.FailureCount{}, err
	}

	return out, nil
}

// PruneFailures implements authgate.FailureCounter. It deletes every window
// that ended at or before the threshold on the clock of the store.
func (s *Store) PruneFailures(ctx context.Context, age time.Duration) (int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var deleted int64

	err := s.inTx(ctx, func(q *db.Queries) error {
		now, err := q.DatabaseNowMs(ctx)
		if err != nil {
			return fmt.Errorf("read the clock of the store: %w", err)
		}

		n, err := q.PruneSignInFailures(ctx, now-age.Milliseconds())
		if err != nil {
			return fmt.Errorf("prune sign-in failures: %w", err)
		}

		deleted = n

		return nil
	})
	if err != nil {
		return 0, err
	}

	return deleted, nil
}

// PruneFailuresAt implements authgate.FailureCounter. It deletes every
// window that ended age or longer ago on now, which the caller passes
// instead of the clock of the store.
func (s *Store) PruneFailuresAt(ctx context.Context, now time.Time, age time.Duration) (int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var deleted int64

	err := s.inTx(ctx, func(q *db.Queries) error {
		n, err := q.PruneSignInFailures(ctx, now.UnixMilli()-age.Milliseconds())
		if err != nil {
			return fmt.Errorf("prune sign-in failures: %w", err)
		}

		deleted = n

		return nil
	})
	if err != nil {
		return 0, err
	}

	return deleted, nil
}

package postgres

import (
	"context"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/internal/authgate"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
)

var _ authgate.FailureCounter = (*Store)(nil)

// AddFailure implements authgate.FailureCounter in one atomic write. Every
// time comes from the clock of the store, read once in the statement, so the
// clock of an instance never moves the window. Concurrent adds each count
// once.
func (s *Store) AddFailure(
	ctx context.Context, source string, length time.Duration,
) (authgate.FailureCount, error) {
	if length < time.Millisecond {
		return authgate.FailureCount{}, authgate.ErrInvalidWindow
	}

	row, err := s.queries.AddSignInFailure(ctx, db.AddSignInFailureParams{
		Source: source, LengthMs: length.Milliseconds(),
	})
	if err != nil {
		return authgate.FailureCount{}, fmt.Errorf("add sign-in failure: %w", err)
	}

	return authgate.FailureCount{
		At:    codec.FromMillis(row.NowMs),
		End:   codec.FromMillis(row.WindowEndMs),
		Count: row.Count,
	}, nil
}

// PruneFailures implements authgate.FailureCounter in one atomic write. It
// deletes every window that ended age or longer ago on the clock of the
// store.
func (s *Store) PruneFailures(ctx context.Context, age time.Duration) (int64, error) {
	deleted, err := s.queries.PruneSignInFailures(ctx, age.Milliseconds())
	if err != nil {
		return 0, fmt.Errorf("prune sign-in failures: %w", err)
	}

	return deleted, nil
}

// PruneFailuresAt implements authgate.FailureCounter in one atomic write. It
// deletes every window that ended age or longer ago on now, which the caller
// passes instead of the clock of the store.
func (s *Store) PruneFailuresAt(ctx context.Context, now time.Time, age time.Duration) (int64, error) {
	deleted, err := s.queries.PruneSignInFailuresAt(ctx, now.UnixMilli()-age.Milliseconds())
	if err != nil {
		return 0, fmt.Errorf("prune sign-in failures: %w", err)
	}

	return deleted, nil
}

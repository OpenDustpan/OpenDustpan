package postgres

import (
	"context"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/internal/store/codec"
	"opendustpan.org/opendustpan/internal/store/postgres/internal/db"
)

var _ principal.WorkingBreakGlassCounter = (*Store)(nil)

// CountWorkingBreakGlass implements principal.WorkingBreakGlassCounter. The
// count reads the rows of the account at the time at.
func (s *Store) CountWorkingBreakGlass(ctx context.Context, at time.Time) (int64, error) {
	return countWorkingBreakGlass(ctx, s.queries, at)
}

// checkWorkingBreakGlassFloor refuses a change that lowers the working
// break-glass count and leaves it below the floor.
func checkWorkingBreakGlassFloor(before, after int64) error {
	if after < before && after < principal.FloorWorkingPaths {
		return fmt.Errorf("restore leaves too few working break-glass accounts: %w",
			principal.ErrNoWorkingBreakGlass)
	}

	return nil
}

// checkBreakGlassPasswords refuses data that holds a break-glass account with
// a local password. A restore writes the break-glass flag and the passwords
// from the backup, so it is a write that can put the flag on a person with a
// password. It reads with the queries of one transaction, and fails closed
// when the count cannot run.
func checkBreakGlassPasswords(ctx context.Context, q *db.Queries) error {
	n, err := q.CountBreakGlassWithPassword(ctx)
	if err != nil {
		return fmt.Errorf("count break-glass accounts with a password: %w: %w",
			principal.ErrBreakGlassUnavailable, err)
	}

	if n > 0 {
		return fmt.Errorf("restore holds %d break-glass accounts with a local password: %w",
			n, principal.ErrBreakGlassPassword)
	}

	return nil
}

// countWorkingBreakGlass counts the working break-glass accounts at the
// time at with the queries of one transaction.
func countWorkingBreakGlass(ctx context.Context, q *db.Queries, at time.Time) (int64, error) {
	n, err := q.CountWorkingBreakGlassPrincipals(ctx, codec.Millis(at))
	if err != nil {
		return 0, fmt.Errorf("count working break-glass accounts: %w: %w", principal.ErrBreakGlassUnavailable, err)
	}

	return n, nil
}

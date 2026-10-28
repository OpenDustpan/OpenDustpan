package sqlite

import (
	"context"
	"fmt"

	"opendustpan.org/opendustpan/pkg/maintenance"
)

// checkSwapMinimumLocked refuses a swap when the binary of this store is
// below the highest minimum binary version of the live database. The swap
// puts in a staged file that this binary built, with its own
// schema_migrations. A binary below the minimum would lower the minimum and
// remove the tables that newer binaries use. The caller holds the write lock,
// so no write can change the live database between the check and the swap.
// The check reads no installation state, so the exemption for maintenance
// mode stays.
func (s *Store) checkSwapMinimumLocked(ctx context.Context) error {
	row, err := s.queries.GetWriteState(ctx)
	if err != nil {
		return fmt.Errorf("read schema minimum sqlite database: %w", maintenance.ErrUnavailable)
	}

	return checkWriteMinimum(row.Minimums, s.binaryVersion)
}

// CheckBinaryMinimum returns a *schema.MinimumError when the binary of this
// store is below the highest minimum binary version of the live database. The
// start of a restore calls it, so a restore on a binary that is too old is
// refused before it stages anything. It reads no installation state, so it
// also works in maintenance mode.
func (s *Store) CheckBinaryMinimum(ctx context.Context) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.checkSwapMinimumLocked(ctx)
}

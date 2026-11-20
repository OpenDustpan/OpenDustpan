package sqlite

import (
	"context"

	"opendustpan.org/opendustpan/pkg/inventory"
)

var _ inventory.SweepLocker = (*Store)(nil)

// TryLockSweep implements inventory.SweepLocker. One server process uses a
// SQLite file, and the process guard refuses a second one, so a lock in the
// store is a lock for every sweep on the file.
func (s *Store) TryLockSweep(_ context.Context) (func(), error) {
	if !s.sweepMu.TryLock() {
		return nil, inventory.ErrSweepRunning
	}

	return s.sweepMu.Unlock, nil
}

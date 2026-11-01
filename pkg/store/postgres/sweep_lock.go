package postgres

import (
	"context"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
)

var _ inventory.SweepLocker = (*Store)(nil)

// sweepLockClass is the first key of the advisory lock that allows one
// retention sweep at a time ("swep" in ASCII). The second key is the schema,
// so two installations in one database do not wait for each other.
const sweepLockClass int32 = 0x73776570

// sweepUnlockTimeout bounds the unlock of the sweep lock after the sweep
// ended, also when its context ended.
const sweepUnlockTimeout = 10 * time.Second

// TryLockSweep implements inventory.SweepLocker with an advisory lock of the
// PostgreSQL session. The lock lives on one connection that the store keeps
// until the release, so every server process on the same database sees it. If
// the connection or the server process dies, PostgreSQL gives up the lock.
func (s *Store) TryLockSweep(ctx context.Context) (func(), error) {
	// The lock connection comes from the long-path helper: it holds no
	// session value, so its statements run under the start-up values of the
	// pool, and its release resets it all the same.
	conn, err := s.longConn(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire sweep lock connection: %w", err)
	}

	q := db.New(conn)

	held, err := q.TryLockSweep(ctx, sweepLockClass)
	if err != nil {
		conn.Release()

		return nil, fmt.Errorf("take sweep lock: %w", err)
	}

	if !held {
		conn.Release()

		return nil, inventory.ErrSweepRunning
	}

	return func() {
		// The sweep context can be done here, so the unlock uses its own bound.
		unlockCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), sweepUnlockTimeout)
		defer cancel()

		unlocked, unlockErr := db.New(conn).UnlockSweep(unlockCtx, sweepLockClass)
		if unlockErr != nil || !unlocked {
			// A connection that holds a lock it cannot unlock must not return
			// to the pool. Closing it makes PostgreSQL give up the lock, so a close
			// error has no effect on the lock.
			s.unmarkForHijack(conn)

			if closeErr := conn.Hijack().Close(unlockCtx); closeErr != nil {
				s.logger.WarnContext(unlockCtx, "sweep lock connection did not close cleanly")
			}

			return
		}

		conn.Release()
	}, nil
}

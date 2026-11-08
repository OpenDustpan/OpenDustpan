package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/sqlite/internal/db"
)

var _ principal.WorkingBreakGlassCounter = (*Store)(nil)

// CountWorkingBreakGlass implements principal.WorkingBreakGlassCounter. The
// count reads the rows of the account at the time at.
func (s *Store) CountWorkingBreakGlass(ctx context.Context, at time.Time) (int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return countWorkingBreakGlass(ctx, s.queries, at)
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

// checkWorkingBreakGlassFloor refuses a change that lowers the working
// break-glass count and leaves it below the floor.
func checkWorkingBreakGlassFloor(before, after int64) error {
	if after < before && after < principal.FloorWorkingPaths {
		return fmt.Errorf("restore leaves too few working break-glass accounts: %w",
			principal.ErrNoWorkingBreakGlass)
	}

	return nil
}

// replayStagedRevocations applies the journaled revocations to the staged
// file and refuses a swap that lowers the working break-glass count below the
// floor. It reads the clock of the store once for both counts.
func replayStagedRevocations(ctx context.Context, tx *sql.Tx) error {
	nowMs, nowErr := storeNow(ctx, db.New(tx))
	if nowErr != nil {
		return nowErr
	}

	at := codec.FromMillis(nowMs)

	before, beforeErr := countWorkingBreakGlassLive(ctx, tx, at)
	if beforeErr != nil {
		return beforeErr
	}

	if err := replayRevocations(ctx, tx); err != nil {
		return err
	}

	after, afterErr := countWorkingBreakGlass(ctx, db.New(tx), at)
	if afterErr != nil {
		return afterErr
	}

	return checkWorkingBreakGlassFloor(before, after)
}

// workingBreakGlassLiveQuery counts the working break-glass accounts of the
// live database that a staged file attaches as live. It holds the same
// predicates as CountWorkingBreakGlassPrincipals.
const workingBreakGlassLiveQuery = `SELECT COUNT(*) FROM live.principals p
WHERE p.break_glass = 1
  AND p.kind = 'person'
  AND p.namespace = ''
  AND p.deleted_at_ms IS NULL
  AND p.disabled_at_ms IS NULL
  AND p.source = 'local'
  AND p.created_at_ms <= ?
  AND EXISTS (
    SELECT 1 FROM live.passkeys pk
    WHERE pk.principal_id = p.id AND pk.suspended_at_ms = 0
      AND pk.created_at_ms <= ?
  )
  AND EXISTS (
    SELECT 1 FROM live.grants g
    WHERE g.object_kind = 'installation'
      AND g.role IN ('site-administrator', 'account-administrator')
      AND g.revoked_at_ms IS NULL
      AND g.condition IS NULL
      AND g.created_at_ms <= ?
      AND (
        (g.subject_kind = 'principal' AND g.subject_id = p.id)
        OR (g.subject_kind = 'group' AND EXISTS (
          SELECT 1 FROM live.group_members m
          JOIN live.principal_groups pg ON pg.id = m.group_id
          WHERE m.group_id = g.subject_id
            AND m.principal_id = p.id
            AND m.removed_at_ms IS NULL
            AND m.state = 'active'
            AND pg.deleted_at_ms IS NULL
            AND m.added_at_ms <= ?
            AND pg.created_at_ms <= ?
        ))
      )
  )`

// countWorkingBreakGlassLive counts the working break-glass accounts at the
// time at of the live database attached to tx, which holds the staged file
// as main.
func countWorkingBreakGlassLive(ctx context.Context, tx *sql.Tx, at time.Time) (int64, error) {
	var n int64

	ms := codec.Millis(at)

	err := tx.QueryRowContext(ctx, workingBreakGlassLiveQuery, ms, ms, ms, ms, ms).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count live working break-glass accounts: %w: %w",
			principal.ErrBreakGlassUnavailable, err)
	}

	return n, nil
}

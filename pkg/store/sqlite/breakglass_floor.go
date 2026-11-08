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

	return countWorkingBreakGlass(ctx, s.conn, breakGlassSchemaMain, at)
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

	before, beforeErr := countWorkingBreakGlass(ctx, tx, breakGlassSchemaLive, at)
	if beforeErr != nil {
		return beforeErr
	}

	if err := replayRevocations(ctx, tx); err != nil {
		return err
	}

	after, afterErr := countWorkingBreakGlass(ctx, tx, breakGlassSchemaMain, at)
	if afterErr != nil {
		return afterErr
	}

	return checkWorkingBreakGlassFloor(before, after)
}

// breakGlassSchema names a schema of the connection: the database of the
// store, or the staged file that a restore attaches as live.
type breakGlassSchema string

const (
	breakGlassSchemaMain breakGlassSchema = "main"
	breakGlassSchemaLive breakGlassSchema = "live"
)

// workingBreakGlassQueryTemplate holds the text of the count for one schema.
const workingBreakGlassQueryTemplate = `SELECT COUNT(*) FROM %[1]s.principals p
WHERE p.break_glass = 1
  AND p.kind = 'person'
  AND p.namespace = ''
  AND p.deleted_at_ms IS NULL
  AND p.disabled_at_ms IS NULL
  AND p.source = 'local'
  AND p.created_at_ms <= ?1
  AND EXISTS (
    SELECT 1 FROM %[1]s.passkeys pk
    WHERE pk.principal_id = p.id AND pk.suspended_at_ms = 0
      AND pk.created_at_ms <= ?1
  )
  AND EXISTS (
    SELECT 1 FROM %[1]s.grants g
    WHERE g.object_kind = 'installation'
      AND g.role IN ('site-administrator', 'account-administrator')
      AND g.revoked_at_ms IS NULL
      AND g.condition IS NULL
      AND g.created_at_ms <= ?1
      AND (
        (g.subject_kind = 'principal' AND g.subject_id = p.id)
        OR (g.subject_kind = 'group' AND EXISTS (
          SELECT 1 FROM %[1]s.group_members m
          JOIN %[1]s.principal_groups pg ON pg.id = m.group_id
          WHERE m.group_id = g.subject_id
            AND m.principal_id = p.id
            AND m.removed_at_ms IS NULL
            AND m.state = 'active'
            AND pg.deleted_at_ms IS NULL
            AND m.added_at_ms <= ?1
            AND pg.created_at_ms <= ?1
        ))
      )
  )`

// workingBreakGlassQuery returns the count query for a schema. The schema is
// one of the fixed constants, so no input reaches the text.
//
// The working break-glass accounts of the installation: an enabled local
// person of the installation with a passkey that is not suspended, and a
// current grant without a condition of an administration role on the
// installation, directly or through an active membership of a group that
// exists. An installation-level IP allowlist must join this count when one
// exists; the current allowlists are per namespace and do not apply to
// installation sign-in.
func workingBreakGlassQuery(schema breakGlassSchema) (string, error) {
	switch schema {
	case breakGlassSchemaMain, breakGlassSchemaLive:
		return fmt.Sprintf(workingBreakGlassQueryTemplate, schema), nil
	default:
		return "", fmt.Errorf("count working break-glass accounts: unknown schema %q: %w",
			string(schema), principal.ErrBreakGlassUnavailable)
	}
}

// countWorkingBreakGlass counts the working break-glass accounts at the
// time at in the schema.
func countWorkingBreakGlass(ctx context.Context, q sqlQueryer, schema breakGlassSchema, at time.Time) (int64, error) {
	query, queryErr := workingBreakGlassQuery(schema)
	if queryErr != nil {
		return 0, queryErr
	}

	var n int64

	err := q.QueryRowContext(ctx, query, codec.Millis(at)).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count working break-glass accounts: %w: %w", principal.ErrBreakGlassUnavailable, err)
	}

	return n, nil
}

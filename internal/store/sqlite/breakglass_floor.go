package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/internal/store/codec"
	"opendustpan.org/opendustpan/internal/store/sqlite/internal/db"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
)

var _ principal.WorkingBreakGlassCounter = (*Store)(nil)

// CountWorkingBreakGlass implements principal.WorkingBreakGlassCounter. The
// count reads the rows of the account at the time at.
func (s *Store) CountWorkingBreakGlass(ctx context.Context, at time.Time) (int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return countWorkingBreakGlass(ctx, s.conn, breakGlassSchemaMain, at)
}

// CountWorkingBreakGlassInNamespace implements
// principal.WorkingBreakGlassCounter. It returns ErrInvalid for an empty ns:
// CountWorkingBreakGlass holds the installation scope.
func (s *Store) CountWorkingBreakGlassInNamespace(ctx context.Context, ns namespace.Name, at time.Time) (int64, error) {
	// Defense in depth: the service does not ask for an empty scope. This
	// check stays so that a new caller cannot read the installation count
	// through the namespace port. It does not rely on the caller.
	if ns == "" {
		return 0, fmt.Errorf("count working break-glass accounts of a namespace: %w", principal.ErrInvalid)
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	return countWorkingBreakGlassInNamespace(ctx, s.conn, breakGlassSchemaMain, ns, at)
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
// floor, or that puts in a break-glass account with a local password. It
// reads the clock of the store once for both counts.
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

	if err := checkBreakGlassPasswords(ctx, db.New(tx)); err != nil {
		return err
	}

	if err := checkNamespaceFloors(ctx, tx, at); err != nil {
		return err
	}

	return checkWorkingBreakGlassFloor(before, after)
}

// checkNamespaceFloors refuses a swap that lowers the working break-glass
// count of a namespace below the floor. The scopes are the namespaces that
// hold a break-glass person in the live or the staged data. The count of one
// namespace never counts the accounts of another namespace or of the
// installation, so a restore that drops the accounts of one namespace is
// refused even when the other scopes hold enough of them.
func checkNamespaceFloors(ctx context.Context, tx *sql.Tx, at time.Time) error {
	scopes, scopesErr := breakGlassRestoreScopes(ctx, tx)
	if scopesErr != nil {
		return scopesErr
	}

	for _, ns := range scopes {
		before, beforeErr := countWorkingBreakGlassInNamespace(ctx, tx, breakGlassSchemaLive, ns, at)
		if beforeErr != nil {
			return beforeErr
		}

		after, afterErr := countWorkingBreakGlassInNamespace(ctx, tx, breakGlassSchemaMain, ns, at)
		if afterErr != nil {
			return afterErr
		}

		if err := checkWorkingBreakGlassFloor(before, after); err != nil {
			return fmt.Errorf("namespace %q: %w", ns, err)
		}
	}

	return nil
}

// breakGlassRestoreScopes returns the namespaces that hold a break-glass
// person in the live or the staged schema, each once, in the order of the
// live scopes first.
func breakGlassRestoreScopes(ctx context.Context, tx *sql.Tx) ([]namespace.Name, error) {
	live, liveErr := breakGlassNamespaceScopes(ctx, tx, breakGlassSchemaLive)
	if liveErr != nil {
		return nil, liveErr
	}

	staged, stagedErr := breakGlassNamespaceScopes(ctx, tx, breakGlassSchemaMain)
	if stagedErr != nil {
		return nil, stagedErr
	}

	seen := make(map[namespace.Name]bool, len(live)+len(staged))
	out := make([]namespace.Name, 0, len(live)+len(staged))

	for _, ns := range append(live, staged...) {
		if !seen[ns] {
			seen[ns] = true
			out = append(out, ns)
		}
	}

	return out, nil
}

// checkBreakGlassPasswords refuses data that holds a break-glass account with
// a local password. A restore writes the break-glass flag and the passwords
// from the backup, so it is a write that can put the flag on a person with a
// password. In the connection of a restore, q reads the staged file. It fails
// closed when the count cannot run.
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

// breakGlassSchema names a schema of the connection. Outside a restore, main
// is the database of the store. In the restore connection, main is the staged
// file and live is the live database that the restore attaches.
type breakGlassSchema string

const (
	breakGlassSchemaMain breakGlassSchema = "main"
	breakGlassSchemaLive breakGlassSchema = "live"
)

// The object kinds that the count reads. The installation scope reads the
// grants on the installation object. A namespace scope reads the grants on
// the namespace object of its own name.
const (
	grantObjectKindInstallation = "installation"
	grantObjectKindNamespace    = "namespace"
)

// workingBreakGlassQueryTemplate holds the text of the count for one schema.
// ?1 is the time in milliseconds, ?2 is the namespace of the scope (empty for
// the installation) and ?3 is the object kind of the grants of the scope.
const workingBreakGlassQueryTemplate = `SELECT COUNT(*) FROM %[1]s.principals p
WHERE p.break_glass = 1
  AND p.kind = 'person'
  AND p.namespace = ?2
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
    WHERE g.object_kind = ?3
      AND g.namespace = ?2
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
// The working break-glass accounts of one scope: an enabled local person of
// the scope with a passkey that is not suspended, and a current grant without
// a condition of an administration role on the scope, directly or through an
// active membership of a group that exists. The scope is the installation
// (empty namespace, grants on the installation) or one namespace (its
// principals, grants on its namespace object). The values come in as
// placeholders. An installation-level IP allowlist must join the installation
// count when one exists; the current allowlists are per namespace and do not
// apply to installation sign-in.
func workingBreakGlassQuery(schema breakGlassSchema) (string, error) {
	switch schema {
	case breakGlassSchemaMain, breakGlassSchemaLive:
		return fmt.Sprintf(workingBreakGlassQueryTemplate, schema), nil
	default:
		return "", fmt.Errorf("count working break-glass accounts: unknown schema %q: %w",
			string(schema), principal.ErrBreakGlassUnavailable)
	}
}

// countWorkingBreakGlass counts the working break-glass accounts of the
// installation at the time at in the schema.
func countWorkingBreakGlass(ctx context.Context, q sqlQueryer, schema breakGlassSchema, at time.Time) (int64, error) {
	return countWorkingBreakGlassInNamespace(ctx, q, schema, "", at)
}

// breakGlassNamespacesQuery holds the text of the namespaces of one schema
// that hold a break-glass person. %[1]s is the schema. The installation is
// no row: its empty namespace is the scope of CountWorkingBreakGlass.
const breakGlassNamespacesQuery = `SELECT DISTINCT p.namespace FROM %[1]s.principals p
WHERE p.break_glass = 1
  AND p.kind = 'person'
  AND p.deleted_at_ms IS NULL
  AND p.namespace <> ''`

// breakGlassNamespaceScopes returns the namespaces of the schema that hold a
// break-glass person, in no order. The schema is one of the fixed constants,
// so no input reaches the text.
func breakGlassNamespaceScopes(ctx context.Context, tx *sql.Tx, schema breakGlassSchema) ([]namespace.Name, error) {
	var text string

	switch schema {
	case breakGlassSchemaMain, breakGlassSchemaLive:
		text = fmt.Sprintf(breakGlassNamespacesQuery, schema)
	default:
		return nil, fmt.Errorf("list the namespaces of the break-glass accounts: unknown schema %q: %w",
			string(schema), principal.ErrBreakGlassUnavailable)
	}

	rows, err := tx.QueryContext(ctx, text)
	if err != nil {
		return nil, fmt.Errorf("list the namespaces of the break-glass accounts: %w: %w",
			principal.ErrBreakGlassUnavailable, err)
	}

	var (
		out     []namespace.Name
		scanErr error
	)

	func() {
		defer func() {
			if closeErr := rows.Close(); closeErr != nil && scanErr == nil {
				scanErr = fmt.Errorf("list the namespaces of the break-glass accounts: %w: %w",
					principal.ErrBreakGlassUnavailable, closeErr)
			}
		}()

		for rows.Next() {
			var ns string

			if err := rows.Scan(&ns); err != nil {
				scanErr = fmt.Errorf("scan a namespace of the break-glass accounts: %w: %w",
					principal.ErrBreakGlassUnavailable, err)

				return
			}

			out = append(out, namespace.Name(ns))
		}

		if err := rows.Err(); err != nil {
			scanErr = fmt.Errorf("list the namespaces of the break-glass accounts: %w: %w",
				principal.ErrBreakGlassUnavailable, err)
		}
	}()

	if scanErr != nil {
		return nil, scanErr
	}

	return out, nil
}

// countWorkingBreakGlassInNamespace counts the working break-glass accounts of
// the scope ns at the time at in the schema. An empty ns is the installation.
func countWorkingBreakGlassInNamespace(
	ctx context.Context, q sqlQueryer, schema breakGlassSchema, ns namespace.Name, at time.Time,
) (int64, error) {
	query, queryErr := workingBreakGlassQuery(schema)
	if queryErr != nil {
		return 0, queryErr
	}

	objectKind := grantObjectKindInstallation
	if ns != "" {
		objectKind = grantObjectKindNamespace
	}

	var n int64

	err := q.QueryRowContext(ctx, query, codec.Millis(at), string(ns), objectKind).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count working break-glass accounts: %w: %w", principal.ErrBreakGlassUnavailable, err)
	}

	return n, nil
}

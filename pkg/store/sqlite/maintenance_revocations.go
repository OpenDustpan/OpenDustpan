package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/sqlite/internal/db"
)

// revocation is one write that only reduces access. The store journals it
// while the installation is in maintenance mode, so a restore can apply it
// again after the swap.
type revocation struct {
	// kind is one of the maintenance.Revoked constants.
	kind string
	// target is the ID of the session, token, principal, grant, join token or
	// provider.
	target string
	// endReason is the end reason of a session, empty for the other kinds.
	endReason string
}

// inRevokingTx runs fn and appends e in the scope @server in one control
// transaction: the change and its audit entry commit together, or neither
// does. The control path has no installation state check, so a write that
// only reduces access runs in maintenance mode: incident response must be
// able to end a session or revoke a token at any time. Only a caller whose
// fn can narrow access may use it. A write that grants or widens access uses
// inAuditedTx, which the state check covers.
//
// The transaction journals rev when the installation is in maintenance mode.
// The caller holds the store lock, as for every write, so a restore swap
// waits for the transaction and the journal it copies is complete.
func (s *Store) inRevokingTx(ctx context.Context, e audit.Entry, rev revocation, fn func(q *db.Queries) error) error {
	return s.inRevokingTxOf(ctx, &e, rev, fn)
}

// inRevokingTxOf is inRevokingTx with an entry that fn may change before the
// append, for example to add changes that only the transaction can read. It
// checks the entry again after fn.
func (s *Store) inRevokingTxOf(
	ctx context.Context,
	e *audit.Entry,
	rev revocation,
	fn func(q *db.Queries) error,
) error {
	if err := validServerEntry(*e); err != nil {
		return err
	}

	return s.inControlTx(ctx, func(q *db.Queries) error {
		if err := journalRevocation(ctx, q, rev, *e); err != nil {
			return err
		}

		if err := fn(q); err != nil {
			return err
		}

		if err := validServerEntry(*e); err != nil {
			return err
		}

		if _, err := appendAuditEntry(ctx, q, *e); err != nil {
			return fmt.Errorf("append audit entry: %w", err)
		}

		return nil
	})
}

// inRevokingFencedTx is inRevokingTx for a revocation in the namespace of
// fence: it checks the fence in the transaction and appends e to the chain of
// that namespace, with no installation state check.
func (s *Store) inRevokingFencedTx(
	ctx context.Context, fence namespace.Fence, e audit.Entry, rev revocation, fn func(q *db.Queries) error,
) error {
	if err := e.Validate(); err != nil {
		return fmt.Errorf("audit entry: %w", err)
	}

	if e.Namespace != fence.Namespace {
		return fmt.Errorf("%w: entry names %q, fence names %q",
			audit.ErrInvalidEntry, e.Namespace, fence.Namespace)
	}

	return s.inControlFencedTx(ctx, fence, func(q *db.Queries) error {
		if err := journalRevocation(ctx, q, rev, e); err != nil {
			return err
		}

		if err := fn(q); err != nil {
			return err
		}

		if _, err := appendAuditEntry(ctx, q, e); err != nil {
			return fmt.Errorf("append audit entry: %w", err)
		}

		return nil
	})
}

// journalRevocation journals rev in the transaction of q when the
// installation is in maintenance mode. In normal operation it writes nothing.
func journalRevocation(ctx context.Context, q *db.Queries, rev revocation, e audit.Entry) error {
	err := q.RecordMaintenanceRevocation(ctx, db.RecordMaintenanceRevocationParams{
		Kind: rev.kind, Target: rev.target, EndReason: rev.endReason, Actor: e.Subject, Object: e.Object,
		AtMs: codec.Millis(e.At),
	})
	if err != nil {
		return fmt.Errorf("journal revocation: %w", err)
	}

	return nil
}

// clearJournalOnEnter starts a new stay in maintenance mode when next is
// maintenance: it clears the journal of revocations, in the transaction of q
// and before the state changes. It clears nothing when the state is
// maintenance already, so a second enter keeps the journal of the stay.
func clearJournalOnEnter(ctx context.Context, q *db.Queries, next maintenance.State) error {
	if next != maintenance.StateMaintenance {
		return nil
	}

	if err := q.ClearMaintenanceRevocationsOnEnter(ctx); err != nil {
		return fmt.Errorf("store installation state sqlite database: %w", maintenance.ErrUnavailable)
	}

	return nil
}

// replayStep is one statement of the replay of a revocation. The parameters
// are ?1 the target and ?2 the time in Unix milliseconds. A statement that
// names ?3 sets third to say what it is: the actor or the end reason.
type replayStep struct {
	sql   string
	third thirdParam
}

// thirdParam selects the value of the parameter ?3 of a replay statement.
type thirdParam int

// The values of the parameter ?3.
const (
	thirdNone thirdParam = iota
	thirdActor
	thirdEndReason
)

// args returns the parameters of the step for the journal row r.
func (st replayStep) args(r maintenance.Revocation) []any {
	args := []any{r.Target, codec.Millis(r.At)}

	if st.third == thirdActor {
		args = append(args, r.Actor)
	}

	if st.third == thirdEndReason {
		args = append(args, r.EndReason)
	}

	return args
}

// replayPlan returns the statements that apply a revocation of kind to the
// staged file (main), or false for an unknown kind. The first statement is the
// one that tells whether the staged data held a live row: the replay writes an
// audit entry only when it changed that row. Every statement only ends,
// revokes or disables, and only where the staged row is still live, so a
// replay never grants access and a second replay changes nothing. A target
// that the backup does not hold matches no row.
func replayPlan(kind string) ([]replayStep, bool) {
	switch kind {
	case maintenance.RevokedSession:
		return []replayStep{
			{
				sql:   `UPDATE main.sessions SET ended_at_ms = ?2, end_reason = ?3 WHERE id = ?1 AND ended_at_ms IS NULL`,
				third: thirdEndReason,
			},
		}, true
	case maintenance.RevokedToken:
		return []replayStep{
			{
				sql:   `UPDATE main.access_tokens SET revoked_at_ms = ?2 WHERE id = ?1 AND revoked_at_ms IS NULL`,
				third: thirdNone,
			},
		}, true
	case maintenance.RevokedPrincipal:
		return []replayStep{
			// The last enabled break-glass account stays enabled, as in
			// DisablePrincipal: a restore must not leave the installation with
			// no way in.
			{sql: `UPDATE main.principals SET disabled_at_ms = ?2, updated_at_ms = ?2
WHERE id = ?1 AND deleted_at_ms IS NULL AND disabled_at_ms IS NULL
  AND (break_glass = 0 OR EXISTS (
    SELECT 1 FROM main.principals o
    WHERE o.id <> principals.id AND o.break_glass = 1 AND o.kind = 'person' AND o.namespace = ''
      AND o.deleted_at_ms IS NULL AND o.disabled_at_ms IS NULL
  ))`, third: thirdNone},
		}, true
	case maintenance.RevokedTempAdmin:
		return tempAdminPlan(), true
	case maintenance.RevokedGrant:
		return []replayStep{
			{
				sql:   `UPDATE main.grants SET revoked_at_ms = ?2, revoked_by = ?3 WHERE id = ?1 AND revoked_at_ms IS NULL`,
				third: thirdActor,
			},
		}, true
	case maintenance.RevokedJoinToken:
		return []replayStep{
			{sql: `UPDATE main.runner_join_tokens SET revoked_at_ms = ?2
WHERE id = ?1 AND revoked_at_ms = 0 AND used_at_ms = 0`, third: thirdNone},
		}, true
	case maintenance.RevokedProvider:
		return []replayStep{
			{sql: `UPDATE main.sign_in_providers SET enabled = 0, updated_at_ms = ?2, version = version + 1
WHERE provider_id = ?1 AND enabled = 1`, third: thirdNone},
			{
				sql: `UPDATE main.sessions SET ended_at_ms = ?2, end_reason = ?3
WHERE ended_at_ms IS NULL AND principal_id IN (
  SELECT id FROM main.principals WHERE substr(subject, 1, length(?1) + 1) = ?1 || ':')`,
				third: thirdEndReason,
			},
		}, true
	case maintenance.RevokedRunner:
		// The retire ends the live claims of the runner, as Runners.Retire
		// does. The first statement tells whether the staged runner was live.
		return []replayStep{
			{sql: `UPDATE main.runners SET retired_at_ms = ?2 WHERE id = ?1 AND retired_at_ms = 0`, third: thirdNone},
			{sql: `UPDATE main.durable_operations SET claim_owner = NULL, claim_expires_at_ms = 0
WHERE state = 'running' AND claim_owner = ?1`, third: thirdNone},
		}, true
	default:
		return nil, false
	}
}

// tempAdminPlan returns the statements of the replay of the expiry of a
// temporary administrator: the whole expiry, as ExpireTempAdmin does it.
func tempAdminPlan() []replayStep {
	return []replayStep{
		{
			sql:   `UPDATE main.temp_admins SET ended_at_ms = ?2 WHERE principal_id = ?1 AND ended_at_ms IS NULL`,
			third: thirdNone,
		},
		{sql: `UPDATE main.principals SET disabled_at_ms = ?2
WHERE id = ?1 AND deleted_at_ms IS NULL AND disabled_at_ms IS NULL`, third: thirdNone},
		{
			sql: `UPDATE main.grants SET revoked_at_ms = ?2, revoked_by = ?3
WHERE subject_kind = '` + string(access.SubjectPrincipal) + `' AND subject_id = ?1 AND revoked_at_ms IS NULL`,
			third: thirdActor,
		},
		{
			sql:   `UPDATE main.group_members SET removed_at_ms = ?2 WHERE principal_id = ?1 AND removed_at_ms IS NULL`,
			third: thirdNone,
		},
		{
			sql: `UPDATE main.access_tokens SET revoked_at_ms = ?2
WHERE principal_id = ?1 AND revoked_at_ms IS NULL AND disabled_at_ms IS NULL AND expires_at_ms > ?2`,
			third: thirdNone,
		},
	}
}

// readJournal returns the rows of the revocation journal of the staged file
// in the order they were written.
func readJournal(ctx context.Context, tx *sql.Tx) ([]maintenance.Revocation, error) {
	const query = `SELECT id, kind, target, end_reason, actor, object, at_ms
FROM main.maintenance_revocations ORDER BY id`

	rows, err := tx.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("read revocation journal: %w", err)
	}

	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			_ = closeErr
		}
	}()

	var out []maintenance.Revocation

	for rows.Next() {
		var (
			r  maintenance.Revocation
			ms int64
		)

		if err := rows.Scan(&r.ID, &r.Kind, &r.Target, &r.EndReason, &r.Actor, &r.Object, &ms); err != nil {
			return nil, fmt.Errorf("read revocation journal: %w", err)
		}

		r.At = codec.FromMillis(ms)
		out = append(out, r)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read revocation journal: %w", err)
	}

	return out, nil
}

// replayRevocations applies the journal to the staged file, in tx, and
// appends one audit entry in the scope @server for each revocation that
// changed a staged row. It runs after copyControlTables, which brings the live
// journal into the staged file, and after writeStagedBookkeeping, which keeps
// the chain state. The restored log replaces the live log, so the entries of
// the original revocations are gone from it. A row of the journal that fails
// its checks (an unknown kind, a target that is not an ID) changes nothing
// and gets an entry with the outcome denied.
func replayRevocations(ctx context.Context, tx *sql.Tx) error {
	journal, err := readJournal(ctx, tx)
	if err != nil {
		return err
	}

	q := db.New(tx)

	for _, r := range journal {
		entry, err := replayRevocation(ctx, tx, r)
		if err != nil {
			return err
		}

		if entry == nil {
			continue
		}

		if _, err := appendAuditEntry(ctx, q, *entry); err != nil {
			return fmt.Errorf("append replay audit entry: %w", err)
		}
	}

	return nil
}

// replayRevocation applies one journal row in tx. It returns the audit entry
// to append, or nil when the staged data held nothing live to change.
func replayRevocation(ctx context.Context, tx *sql.Tx, r maintenance.Revocation) (*audit.Entry, error) {
	if err := r.Validate(); err != nil {
		return checkedReplayEntry(r, err)
	}

	plan, known := replayPlan(r.Kind)
	if !known {
		return checkedReplayEntry(r, fmt.Errorf("%w: unknown kind %q", maintenance.ErrInvalidRevocation, r.Kind))
	}

	applied, err := applyReplayPlan(ctx, tx, r, plan)
	if err != nil {
		return nil, err
	}

	if !applied && r.Kind == maintenance.RevokedPrincipal {
		entry, kept, err := keptBreakGlassEntry(ctx, tx, r)
		if err != nil {
			return nil, err
		}

		if kept {
			return &entry, nil
		}
	}

	if !applied {
		return nil, nil //nolint:nilnil // No staged row changed: the caller appends no entry.
	}

	return checkedReplayEntry(r, nil)
}

// applyReplayPlan runs the statements of plan for r and reports whether the
// first one changed a staged row.
func applyReplayPlan(ctx context.Context, tx *sql.Tx, r maintenance.Revocation, plan []replayStep) (bool, error) {
	applied := false

	for i, step := range plan {
		res, err := tx.ExecContext(ctx, step.sql, step.args(r)...)
		if err != nil {
			return false, fmt.Errorf("replay revocation %d (%s): %w", r.ID, r.Kind, err)
		}

		n, err := res.RowsAffected()
		if err != nil {
			return false, fmt.Errorf("replay revocation %d (%s): %w", r.ID, r.Kind, err)
		}

		if i == 0 && n > 0 {
			applied = true
		}
	}

	return applied, nil
}

// keptBreakGlassEntry returns the entry of a replayed disable that changed no
// row. When the target is an enabled break-glass account of the staged data,
// the conditional update kept it as the last one: the entry has the outcome
// denied and the reason principal.no-working-break-glass. Otherwise the target was
// disabled, ended or not in the staged data, and kept is false.
func keptBreakGlassEntry(ctx context.Context, tx *sql.Tx, r maintenance.Revocation) (audit.Entry, bool, error) {
	var none audit.Entry

	n, err := db.New(tx).CountEnabledBreakGlassPrincipal(ctx, r.Target)
	if err != nil {
		return none, false, fmt.Errorf("replay revocation %d (%s): %w", r.ID, r.Kind, err)
	}

	if n == 0 {
		return none, false, nil
	}

	entry := r.KeptEntry(maintenance.ReasonNoWorkingBreakGlass)
	if err = entry.Validate(); err != nil {
		return none, false, fmt.Errorf("replay revocation %d: %w", r.ID, err)
	}

	return entry, true, nil
}

// checkedReplayEntry returns the replay entry of r after Validate accepts it,
// or the fixed denied entry. A row that passed its own checks has already
// ended its target: a revocation only narrows access, so a failed entry
// never undoes it.
func checkedReplayEntry(r maintenance.Revocation, invalid error) (*audit.Entry, error) {
	entry, err := r.CheckedReplayEntry(invalid, audit.Entry.Validate)
	if err != nil {
		return nil, fmt.Errorf("replay revocation %d: %w", r.ID, err)
	}

	return &entry, nil
}

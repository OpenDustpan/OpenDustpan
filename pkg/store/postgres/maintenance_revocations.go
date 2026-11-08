package postgres

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
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
// The transaction first journals rev when the installation is in maintenance
// mode. The journal insert queues behind the lock that a restore takes on the
// journal for its swap, so the change that follows lands in the restored
// schema, never in the one that the swap drops. Then it takes the advisory
// lock of the scope, as every audited change does.
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

		// This lock comes before fn on purpose. It also serializes the writes
		// that count other rows in the same transaction: the disable of a
		// principal checks the last site administrator and the last enabled
		// break-glass account. Under READ COMMITTED, two such writes without
		// it both pass the check. Do not move it after fn.
		if err := q.LockServerAuditScope(ctx); err != nil {
			return fmt.Errorf("lock server audit scope: %w", err)
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
// fence: it appends e to the chain of that namespace and checks the fence
// with the namespace row held FOR UPDATE, as inLockedFencedAuditedTx does,
// with no installation state check. It takes the advisory lock of @server
// first, so a revocation orders with the writes of the installation, then
// the namespace row. No transaction takes the lock after a namespace row.
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

	return s.inControlTx(ctx, func(q *db.Queries) error {
		if err := journalRevocation(ctx, q, rev, e); err != nil {
			return err
		}

		if err := q.LockServerAuditScope(ctx); err != nil {
			return fmt.Errorf("lock server audit scope: %w", err)
		}

		if err := checkFenceForUpdate(ctx, q, fence); err != nil {
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

// lockSwap takes the swap lock in ACCESS EXCLUSIVE mode for the rest of the
// swap transaction tx. The lock is the revocation journal, in the control
// schema, which the swap never renames. A swap takes it before it touches a
// live table:
//
//   - A snapshot read takes the same lock in ACCESS SHARE mode before its
//     snapshot (see inSnapshotTx). So the swap waits for each snapshot read in
//     flight, while it holds no live table, and a snapshot read that starts
//     later waits for the swap to commit. A read cannot deadlock with the
//     swap, and it cannot see the swapped tables without the changes of the
//     swap.
//   - A write to the journal waits until tx ends, and tx waits for the writes
//     in flight, so the replay reads every journaled revocation and no later
//     one lands in the schema that the swap drops.
//
// A waiting ACCESS EXCLUSIVE request makes PostgreSQL queue every later
// snapshot read behind it. So the swap never waits long: each attempt has a
// short lock timeout, and a failed attempt leaves the queue. The swap tries
// again, with backoff and jitter, until ctx ends. Then it returns an error
// that names the cause: a long snapshot read, for example a backup.
func lockSwap(ctx context.Context, tx pgx.Tx, live string) error {
	var previous string
	if err := tx.QueryRow(ctx, "SELECT current_setting('lock_timeout')").Scan(&previous); err != nil {
		return fmt.Errorf("read lock timeout: %w", err)
	}

	query := "LOCK TABLE " + swapLockTable(live) + " IN ACCESS EXCLUSIVE MODE"
	backoff := swapLockBackoffStart

	for {
		err := tryLockSwap(ctx, tx, query)
		if err == nil {
			// The attempt set the timeout for this transaction. Put the
			// earlier value back for the rest of the swap.
			if _, resetErr := tx.Exec(ctx, "SELECT set_config('lock_timeout', $1, true)", previous); resetErr != nil {
				return fmt.Errorf("restore lock timeout: %w", resetErr)
			}

			return nil
		}

		// The context can end inside the attempt, as a wait or as a driver
		// error. Then the lock is still held by a long read.
		if ctx.Err() != nil {
			return errSwapLockBusy(ctx)
		}

		if !isLockTimeout(err) {
			return fmt.Errorf("lock restore swap: %w", err)
		}

		select {
		case <-time.After(swapJitter(backoff)):
		case <-ctx.Done():
			return errSwapLockBusy(ctx)
		}

		backoff = min(backoff*backoffGrowth, swapLockBackoffMax)
	}
}

// swapJitter returns a wait from half of backoff to backoff. The jitter keeps
// two restores from retrying in step. When the random source fails, it
// returns half of backoff, with no jitter.
func swapJitter(backoff time.Duration) time.Duration {
	half := backoff / backoffJitterDivisor

	n, err := rand.Int(rand.Reader, big.NewInt(int64(half)+1))
	if err != nil {
		return half
	}

	return half + time.Duration(n.Int64())
}

// errSwapLockBusy returns the error of a swap that did not get the lock
// before ctx ended.
func errSwapLockBusy(ctx context.Context) error {
	return fmt.Errorf("lock restore swap: a snapshot read, for example a backup, holds the lock;"+
		" wait for it to end, then run the restore again: %w", context.Cause(ctx))
}

// The timing of the attempts of lockSwap. An attempt waits at most
// swapLockTimeout, so a snapshot read waits at most that long behind a
// waiting swap.
const (
	swapLockTimeout      = 100 * time.Millisecond
	swapLockBackoffStart = 50 * time.Millisecond
	swapLockBackoffMax   = time.Second
	backoffGrowth        = 2
	backoffJitterDivisor = 2
)

// sqlstateLockNotAvailable is the SQLSTATE of a lock wait that ended at the
// lock timeout.
const sqlstateLockNotAvailable = "55P03"

// isLockTimeout reports whether err is a lock wait that ended at the lock
// timeout.
func isLockTimeout(err error) bool {
	var pgErr *pgconn.PgError

	return errors.As(err, &pgErr) && pgErr.Code == sqlstateLockNotAvailable
}

// tryLockSwap runs one attempt of lockSwap in a savepoint, so that a lock
// timeout does not end tx.
func tryLockSwap(ctx context.Context, tx pgx.Tx, query string) (err error) {
	attempt, err := tx.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin lock attempt: %w", err)
	}

	defer func() {
		if err != nil {
			//nolint:errcheck // The attempt failed; its error is the one to report.
			_ = attempt.Rollback(context.WithoutCancel(ctx))
		}
	}()

	timeout := strconv.FormatInt(swapLockTimeout.Milliseconds(), 10) + "ms"

	if _, err = attempt.Exec(ctx, "SELECT set_config('lock_timeout', $1, true)", timeout); err != nil {
		return fmt.Errorf("set lock timeout: %w", err)
	}

	if _, err = attempt.Exec(ctx, query); err != nil {
		return err //nolint:wrapcheck // The caller reads the SQLSTATE.
	}

	if err = attempt.Commit(ctx); err != nil {
		return fmt.Errorf("release lock attempt: %w", err)
	}

	return nil
}

// swapLockTable returns the qualified name of the table that serves as the
// swap lock: the revocation journal. The name is qualified, so a table of the
// same name in the live schema cannot take its place.
//
// A future ALTER TABLE of maintenance_revocations takes ACCESS EXCLUSIVE on
// it, and then every snapshot read waits until that ALTER ends. Keep such a
// change short, or give the swap lock a table of its own.
func swapLockTable(live string) string {
	return revocationJournal(live)
}

// revocationJournal returns the qualified name of the journal: the table
// lives in the control schema of the live schema, which a swap never renames.
func revocationJournal(live string) string {
	return pgIdent(live+"_control") + ".maintenance_revocations"
}

// replayStep is one statement of the replay of a revocation. The parameters
// are $1 the target and $2 the time in Unix milliseconds. A statement that
// names $3 sets third to say what it is: the actor or the end reason.
type replayStep struct {
	sql   string
	third thirdParam
}

// thirdParam selects the value of the parameter $3 of a replay statement.
type thirdParam int

// The values of the parameter $3.
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
// schema live, or false for an unknown kind. The first statement is the one
// that tells whether the restored data held a live row: the replay writes an
// audit entry only when it changed that row. Every statement only ends,
// revokes or disables, and only where the restored row is still live, so a
// replay never grants access and a second replay changes nothing. A target
// that the backup does not hold matches no row.
func replayPlan(live, kind string) ([]replayStep, bool) {
	schema := pgIdent(live)

	switch kind {
	case maintenance.RevokedSession:
		return []replayStep{
			{sql: "UPDATE " + schema + ".sessions SET ended_at_ms = $2, end_reason = $3 " +
				"WHERE id = $1 AND ended_at_ms IS NULL", third: thirdEndReason},
		}, true
	case maintenance.RevokedToken:
		return []replayStep{
			{sql: "UPDATE " + schema + ".access_tokens SET revoked_at_ms = $2 " +
				"WHERE id = $1 AND revoked_at_ms IS NULL", third: thirdNone},
		}, true
	case maintenance.RevokedPrincipal:
		return []replayStep{
			// The last enabled break-glass account stays enabled, as in
			// DisablePrincipal: a restore must not leave the installation with
			// no way in.
			{sql: "UPDATE " + schema + ".principals AS p SET disabled_at_ms = $2, updated_at_ms = $2 " +
				"WHERE p.id = $1 AND p.deleted_at_ms IS NULL AND p.disabled_at_ms IS NULL " +
				"AND (NOT p.break_glass OR EXISTS (SELECT 1 FROM " + schema + ".principals o " +
				"WHERE o.id <> p.id AND o.break_glass AND o.kind = 'person' AND o.namespace = '' " +
				"AND o.deleted_at_ms IS NULL AND o.disabled_at_ms IS NULL))", third: thirdNone},
		}, true
	case maintenance.RevokedTempAdmin:
		return tempAdminPlan(schema), true
	case maintenance.RevokedGrant:
		// The live check is num_nulls(revoked_at_ms) = 1, not IS NULL, so no
		// partial index of the live grants can serve this lookup by ID.
		// Only NULL reads as live: a stored -1 or 0 does not, as with IS NULL.
		return []replayStep{
			{sql: "UPDATE " + schema + ".grants SET revoked_at_ms = $2, revoked_by = $3 " +
				"WHERE id = $1 AND num_nulls(revoked_at_ms) = 1", third: thirdActor},
		}, true
	case maintenance.RevokedJoinToken:
		return []replayStep{
			{sql: "UPDATE " + schema + ".runner_join_tokens SET revoked_at_ms = $2 " +
				"WHERE id = $1 AND revoked_at_ms = 0 AND used_at_ms = 0", third: thirdNone},
		}, true
	case maintenance.RevokedProvider:
		return []replayStep{
			{sql: "UPDATE " + schema + ".sign_in_providers SET enabled = FALSE, updated_at_ms = $2, " +
				"version = version + 1 WHERE provider_id = $1 AND enabled", third: thirdNone},
			{
				sql: "UPDATE " + schema + ".sessions SET ended_at_ms = $2, end_reason = $3 " +
					"WHERE ended_at_ms IS NULL AND principal_id IN " +
					"(SELECT id FROM " + schema + ".principals WHERE starts_with(subject, $1::text || ':'))",
				third: thirdEndReason,
			},
		}, true
	case maintenance.RevokedRunner:
		// The retire ends the live claims of the runner, as Runners.Retire
		// does. The first statement tells whether the restored runner was
		// live.
		return []replayStep{
			{
				sql:   "UPDATE " + schema + ".runners SET retired_at_ms = $2 WHERE id = $1 AND retired_at_ms = 0",
				third: thirdNone,
			},
			{sql: "UPDATE " + schema + ".durable_operations SET claim_owner = NULL, claim_expires_at_ms = 0 " +
				"WHERE state = 'running' AND claim_owner = $1::text AND $2::bigint > 0", third: thirdNone},
		}, true
	default:
		return nil, false
	}
}

// tempAdminPlan returns the statements of the replay of the expiry of a
// temporary administrator: the whole expiry, as ExpireTempAdmin does it.
func tempAdminPlan(schema string) []replayStep {
	return []replayStep{
		{sql: "UPDATE " + schema + ".temp_admins SET ended_at_ms = $2 " +
			"WHERE principal_id = $1 AND ended_at_ms IS NULL", third: thirdNone},
		{sql: "UPDATE " + schema + ".principals SET disabled_at_ms = $2 " +
			"WHERE id = $1 AND deleted_at_ms IS NULL AND disabled_at_ms IS NULL", third: thirdNone},
		{sql: "UPDATE " + schema + ".grants SET revoked_at_ms = $2, revoked_by = $3 " +
			"WHERE subject_kind = '" + string(access.SubjectPrincipal) + "' AND subject_id = $1 " +
			"AND revoked_at_ms IS NULL", third: thirdActor},
		{sql: "UPDATE " + schema + ".group_members SET removed_at_ms = $2 " +
			"WHERE principal_id = $1 AND removed_at_ms IS NULL", third: thirdNone},
		{
			sql: "UPDATE " + schema + ".access_tokens SET revoked_at_ms = $2 " +
				"WHERE principal_id = $1 AND revoked_at_ms IS NULL AND disabled_at_ms IS NULL AND expires_at_ms > $2",
			third: thirdNone,
		},
	}
}

// readJournal returns the rows of the revocation journal in the order they
// were written.
func readJournal(ctx context.Context, tx pgx.Tx, live string) ([]maintenance.Revocation, error) {
	query := "SELECT id, kind, target, end_reason, actor, object, at_ms FROM " + revocationJournal(
		live,
	) + " ORDER BY id"

	rows, err := tx.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("read revocation journal: %w", err)
	}

	defer rows.Close()

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

// replayRevocations applies the journal to the restored schema live, in tx,
// and appends one audit entry in the scope @server for each revocation that
// changed a restored row. The restored log replaces the live log, so the
// entries of the original revocations are gone from it. A row of the journal
// that fails its checks (an unknown kind, a target that is not an ID) changes
// nothing and gets an entry with the outcome denied. The entries follow the
// chain state that the swap kept.
func (s *Store) replayRevocations(ctx context.Context, tx pgx.Tx, live string) error {
	journal, err := readJournal(ctx, tx, live)
	if err != nil {
		return err
	}

	q := s.queries.WithTx(tx)

	for _, r := range journal {
		entry, err := replayRevocation(ctx, tx, live, r)
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
// to append, or nil when the restored data held nothing live to change.
func replayRevocation(ctx context.Context, tx pgx.Tx, live string, r maintenance.Revocation) (*audit.Entry, error) {
	if err := r.Validate(); err != nil {
		return checkedReplayEntry(r, err)
	}

	plan, known := replayPlan(live, r.Kind)
	if !known {
		return checkedReplayEntry(r, fmt.Errorf("%w: unknown kind %q", maintenance.ErrInvalidRevocation, r.Kind))
	}

	applied, err := applyReplayPlan(ctx, tx, r, plan)
	if err != nil {
		return nil, err
	}

	if !applied && r.Kind == maintenance.RevokedPrincipal {
		entry, kept, err := keptBreakGlassEntry(ctx, tx, live, r)
		if err != nil {
			return nil, err
		}

		if kept {
			return &entry, nil
		}
	}

	if !applied {
		return nil, nil //nolint:nilnil // No restored row changed: the caller appends no entry.
	}

	return checkedReplayEntry(r, nil)
}

// applyReplayPlan runs the statements of plan for r and reports whether the
// first one changed a restored row.
func applyReplayPlan(ctx context.Context, tx pgx.Tx, r maintenance.Revocation, plan []replayStep) (bool, error) {
	applied := false

	for i, step := range plan {
		tag, err := tx.Exec(ctx, step.sql, step.args(r)...)
		if err != nil {
			return false, fmt.Errorf("replay revocation %d (%s): %w", r.ID, r.Kind, err)
		}

		if i == 0 && tag.RowsAffected() > 0 {
			applied = true
		}
	}

	return applied, nil
}

// keptBreakGlassEntry returns the entry of a replayed disable that changed no
// row. When the target is an enabled break-glass account of the restored data,
// the conditional update kept it as the last one: the entry has the outcome
// denied and the reason principal.no-working-break-glass. Otherwise the target was
// disabled, ended or not in the restored data, and kept is false.
func keptBreakGlassEntry(
	ctx context.Context, tx pgx.Tx, live string, r maintenance.Revocation,
) (audit.Entry, bool, error) {
	var (
		none audit.Entry
		n    int64
	)

	err := tx.QueryRow(ctx, "SELECT COUNT(*) FROM "+pgIdent(live)+".principals "+
		"WHERE id = $1 AND namespace = '' AND break_glass AND deleted_at_ms IS NULL AND disabled_at_ms IS NULL",
		r.Target).Scan(&n)
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

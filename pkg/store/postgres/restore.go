package postgres

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
)

var (
	_ backup.RestoreOperations    = (*Store)(nil)
	_ backup.RestoreStaging       = (*Store)(nil)
	_ backup.Applier              = (*Store)(nil)
	_ backup.AccessKeepingApplier = (*Store)(nil)
	_ backup.Fencer               = (*Store)(nil)
)

// GetRestore implements backup.RestoreOperations: it returns the restore row
// id, or ErrRestoreNotFound.
func (s *Store) GetRestore(ctx context.Context, id backup.RestoreID) (backup.RestoreRecord, error) {
	row, err := s.queries.GetRestore(ctx, id.String())
	if errors.Is(err, pgx.ErrNoRows) {
		return backup.RestoreRecord{}, backup.ErrRestoreNotFound
	}

	if err != nil {
		return backup.RestoreRecord{}, fmt.Errorf("select restore: %w", err)
	}

	return restoreRecord(row)
}

// ListRestores implements backup.RestoreOperations: every restore row, newest
// first.
func (s *Store) ListRestores(ctx context.Context) ([]backup.RestoreRecord, error) {
	rows, err := s.queries.ListRestores(ctx)
	if err != nil {
		return nil, fmt.Errorf("select restores: %w", err)
	}

	out := make([]backup.RestoreRecord, 0, len(rows))

	for i := range rows {
		r, err := restoreRecord(db.GetRestoreRow(rows[i]))
		if err != nil {
			return nil, err
		}

		out = append(out, r)
	}

	return out, nil
}

// restoreRecord converts a restore row into a domain record. A NULL end time
// reads as the zero time: the restore still runs. An unknown state reports a
// corrupt row: every writer validates the state first.
func restoreRecord(row db.GetRestoreRow) (backup.RestoreRecord, error) {
	var endedAt time.Time

	if row.EndedAtMs.Valid {
		endedAt = codec.FromMillis(row.EndedAtMs.Int64)
	}

	return codec.FromRestoreRow(codec.RestoreRow{
		ID:          row.ID,
		State:       row.State,
		BackupID:    row.BackupID,
		PreBackupID: row.PreBackupID,
		RequestedBy: row.RequestedBy,
		Error:       row.Error,
		StartedAtMs: row.StartedAtMs,
		EndedAtMs:   codec.OptionalMillis(endedAt),
		DevReport:   row.DevReport,
	})
}

// ApplyStaging implements backup.Applier: it swaps the staged restore schema
// into the live schema in one transaction (ADR 38 decision 6) and drops the
// staging area. The staged schema passed every check in StagePortable, so the
// swap cannot mix two installations. With no staged schema it reports
// ErrCorrupt and changes nothing.
func (s *Store) ApplyStaging(ctx context.Context) error {
	s.stagingMu.Lock()
	defer s.stagingMu.Unlock()

	if s.stagingSchema == "" {
		return backup.ErrCorrupt
	}

	staging := s.stagingSchema

	plan := swapPlan{staging: staging, previous: "", keepAccess: false, before: nil, after: nil}
	if _, err := s.swapStaging(ctx, plan); err != nil {
		return err
	}

	s.stagingSchema = ""

	return nil
}

// swapPlan is one swap of a staging schema into the live schema.
type swapPlan struct {
	// staging is the staging schema that goes live.
	staging string
	// previous is the name of the old live schema after the swap. Empty
	// means the name of the live schema with the suffix _previous, and the
	// swap drops the old live schema in its transaction. A restore that runs
	// as a durable operation names it, and its cleanup drops it later.
	previous string
	// keepAccess keeps the current rows of the access tables.
	keepAccess bool
	// before runs in the swap transaction after the swap lock and before
	// the first change: the first check of the step fence, with no lock on
	// the row of the operation, and the check of the installation state.
	before func(tx pgx.Tx) error
	// after runs at the end of the swap transaction, after the copy of the
	// durable control tables, with the result of the swap: the check of the
	// step fence and the record of the step.
	after func(tx pgx.Tx, kept backup.KeepAccessResult) error
}

// swapStaging renames the live schema aside, renames the staging schema to
// the live name, keeps the control tables of the live installation, and
// increments the failover version of every namespace, all in one
// transaction (ADR 38 decisions 6 and 7). With plan.keepAccess it keeps the
// current rows of the access tables. Without plan.previous it drops the old
// live schema in the same transaction. A failure rolls back and leaves the
// live schema in place. The staging schema carries its own
// schema_migrations, so the swapped schema keeps its version. Fencing in the
// same transaction means a crash after the swap cannot leave restored data
// with old failover versions: either both land or neither does. The SQLite
// adapter fences the staged file before its rename for the same guarantee: a
// file rename cannot share a transaction with SQL, so it carries
// already-fenced versions instead. It returns the count of the dropped
// temp_admins rows of a swap that keeps the access tables, and the count of
// the update leases that the swap expired.
func (s *Store) swapStaging(ctx context.Context, plan swapPlan) (backup.KeepAccessResult, error) {
	if !hasStagingPrefix(plan.staging) {
		return backup.KeepAccessResult{}, fmt.Errorf("apply staging schema %q: %w", plan.staging, backup.ErrCorrupt)
	}

	opts := pgx.TxOptions{
		IsoLevel:       pgx.ReadCommitted,
		AccessMode:     pgx.ReadWrite,
		DeferrableMode: pgx.NotDeferrable,
		BeginQuery:     "",
		CommitQuery:    "",
	}

	var kept backup.KeepAccessResult

	err := s.inLongTx(ctx, backupBound(), opts, func(tx pgx.Tx) error {
		return s.swapTx(ctx, tx, plan, &kept)
	})

	return kept, err
}

// swapTx is the transaction of swapStaging on tx. It sets kept as swapIn
// does.
//
// The swap takes the advisory lock of the scope @server first, before the swap
// lock on the revocation journal. A write that runs in maintenance mode takes
// the same advisory lock before it inserts into the journal, so the swap and
// those writes cannot deadlock. The lock lasts to the commit of the swap.
func (s *Store) swapTx(ctx context.Context, tx pgx.Tx, plan swapPlan, kept *backup.KeepAccessResult) error {
	run := func(_ context.Context, fn func(q *db.Queries) error) error {
		return fn(s.queries.WithTx(tx))
	}

	return runServerAuditTx(ctx, run, func(srv serverAuditTx) error {
		return s.swapLockedTx(ctx, tx, srv, plan, kept)
	})
}

// swapLockedTx is swapTx after the transaction took the advisory lock of the
// server audit scope.
func (s *Store) swapLockedTx(
	ctx context.Context, tx pgx.Tx, srv serverAuditTx, plan swapPlan, kept *backup.KeepAccessResult,
) error {
	var live string

	if err := tx.QueryRow(ctx, "SELECT current_schema()").Scan(&live); err != nil {
		return fmt.Errorf("read live schema: %w", err)
	}

	if live == "" || live == plan.staging {
		return fmt.Errorf("apply staging schema %q over %q: %w", plan.staging, live, backup.ErrCorrupt)
	}

	// The swap lock comes before the swap touches a live table, so that no
	// snapshot read overlaps the swap.
	if err := lockSwap(ctx, tx, live); err != nil {
		return err
	}

	if plan.before != nil {
		if err := plan.before(tx); err != nil {
			return err
		}
	}

	// The checks cover the minimum binary version of the live schema, so
	// that a binary below it cannot swap in a schema that it built, and the
	// whole staging area, in the swap transaction, before the first rename,
	// also the access tables that a swap that keeps them does not take.
	if err := s.checkSwap(ctx, tx, plan.staging); err != nil {
		return err
	}

	if s.swapHook != nil {
		s.swapHook(ctx)
	}

	if err := s.swapInWithBreakGlassFloor(ctx, tx, srv, swapInTarget{plan: plan, live: live}, kept); err != nil {
		return err
	}

	if plan.after != nil {
		return plan.after(tx, *kept)
	}

	return nil
}

// swapInWithBreakGlassFloor swaps in.plan.staging in for in.live, and refuses a
// swap that lowers the working break-glass count below the floor. It counts
// the live data before the swap and the data that is live after it, in the
// same transaction.
func (s *Store) swapInWithBreakGlassFloor(
	ctx context.Context, tx pgx.Tx, srv serverAuditTx, in swapInTarget, kept *backup.KeepAccessResult,
) error {
	nowMs, nowErr := storeNow(ctx, s.queries.WithTx(tx))
	if nowErr != nil {
		return nowErr
	}

	at := codec.FromMillis(nowMs)

	before, beforeErr := countWorkingBreakGlass(ctx, s.queries.WithTx(tx), at)
	if beforeErr != nil {
		return beforeErr
	}

	if err := s.swapIn(ctx, tx, srv, in, kept); err != nil {
		return err
	}

	after, afterErr := countWorkingBreakGlass(ctx, s.queries.WithTx(tx), at)
	if afterErr != nil {
		return afterErr
	}

	return checkWorkingBreakGlassFloor(before, after)
}

// swapInTarget is the target of swapIn: the plan of the swap and the live
// schema of the installation.
type swapInTarget struct {
	plan swapPlan
	live string
}

// swapIn swaps in.plan.staging in for in.live in tx, after the swap lock and
// the checks, and sets kept to the count of the dropped temp_admins rows of
// a swap that keeps the access tables and to the count of the expired update
// leases. srv is the locked server audit transaction of tx.
func (s *Store) swapIn(
	ctx context.Context, tx pgx.Tx, srv serverAuditTx, in swapInTarget, kept *backup.KeepAccessResult,
) error {
	if in.plan.keepAccess {
		return s.keepAccessTx(ctx, tx, srv, in, kept)
	}

	previous, drop := previousSchema(in.live, in.plan.previous)

	if _, err := tx.Exec(ctx, "ALTER SCHEMA "+pgIdent(in.live)+" RENAME TO "+pgIdent(previous)); err != nil {
		return fmt.Errorf("move live schema aside: %w", err)
	}

	if _, err := tx.Exec(ctx, "ALTER SCHEMA "+pgIdent(in.plan.staging)+" RENAME TO "+pgIdent(in.live)); err != nil {
		return fmt.Errorf("move staged schema live: %w", err)
	}

	return s.finishSwap(ctx, tx, srv, swapNames{
		live: in.live, previous: previous, staging: in.plan.staging, drop: drop,
	}, kept)
}

// previousSchema returns the name of the old live schema after a swap, and
// whether the swap drops it: the name that the plan gives, kept for the
// cleanup, or the live name with the suffix _previous, dropped at once.
func previousSchema(live, planned string) (string, bool) {
	if planned != "" {
		return planned, false
	}

	return live + "_previous", true
}

// swapNames are the schemas of one swap: the live name, the name of the old
// live schema, the staging schema, and whether the swap drops the old live
// schema.
type swapNames struct {
	live     string
	previous string
	staging  string
	drop     bool
}

// finishSwap ends the swap in tx, with srv the locked server audit
// transaction of tx: it keeps the audit chain state of this
// installation, keeps the durable control tables of the live installation,
// applies the journaled revocations again, drops the schema previous when
// names.drop says so and the control schema of the staging area, and fences
// the restored schema live (see fenceRestored). It sets kept.ExpiredUpdateLeases. The installation, backups,
// restores and revocation journal tables live in the control schema, which
// the swap never renames, so they stay across the swap with no copy.
//
// The check of the step fence under the lock of the row of the operation
// comes after all of this, at the end of the swap transaction. So a renewal
// of the claim of the restore does not wait for the checks of the staging
// area and the renames. It still waits from the lock of the durable control
// tables in keepControlTables to the commit: through the copy of those
// tables, the replay of the revocations and the fence of the namespaces.
// That time grows with the size of the installation.
func (s *Store) finishSwap(
	ctx context.Context, tx pgx.Tx, srv serverAuditTx, names swapNames, kept *backup.KeepAccessResult,
) error {
	live, previous, staging := names.live, names.previous, names.staging

	if err := keepChainState(ctx, tx, live, previous); err != nil {
		return err
	}

	if err := keepControlTables(ctx, tx, srv, live, previous); err != nil {
		return err
	}

	// The restored access tables come from the backup. Apply again the
	// revocations that ran in this stay in maintenance mode, so the swap does not
	// bring back a session or a token that an operator ended.
	if err := s.replayRevocations(ctx, srv, tx, live); err != nil {
		return err
	}

	// The drop stays in the swap transaction. It waits for each statement in
	// flight that still reads an old table, so no statement reads the old
	// tables after the swap commits. A restore that runs as a durable
	// operation keeps the old schema for its cleanup step instead.
	if names.drop {
		if _, err := tx.Exec(ctx, "DROP SCHEMA "+pgIdent(previous)+" CASCADE"); err != nil {
			return fmt.Errorf("drop previous schema: %w", err)
		}
	}

	if _, err := tx.Exec(ctx, "DROP SCHEMA IF EXISTS "+pgIdent(staging+"_control")+" CASCADE"); err != nil {
		return fmt.Errorf("drop staging control schema: %w", err)
	}

	if err := fenceRestored(ctx, tx, live, kept); err != nil {
		return err
	}

	return deriveUnitState(ctx, tx, live, s.logger)
}

// deriveUnitState records the state version of every unit of the restored
// schema live in unit_state, in tx: a restore derives the version from the
// restored rows and brings no stored version back. A workspace takes the state
// version that its row makes current, a stack the digest of its last
// checkpoint.
//
// A restored version is not final: it predates the delivery of any waiting
// job, so the final fields stay empty and a write-back job that waits for a
// state version keeps waiting. The write is one statement for the workspaces
// and one for the stacks, through the one writer of unit_state.
func deriveUnitState(ctx context.Context, tx pgx.Tx, live string, logger *slog.Logger) error {
	return publishUnitState(ctx, db.New(tx), unitStateWrite{
		kind: unitStateWriteRestore, nsName: "", unitID: "", version: "", final: false,
		origin: identity.StateOrigin{Job: "", Commit: ""}, atMs: 0, exec: tx, schema: live, logger: logger,
	})
}

// fenceRestored increments the failover version of every namespace of the
// restored schema live, releases the workspace locks, expires the lease of
// every running update and resets the audit sink cursors and leases, in tx. It
// sets kept.ExpiredUpdateLeases to the count of the expired leases. The
// control tables stay across the swap, so the running restore row of this
// installation stays readable during the swap and no restored running row
// needs failing.
func fenceRestored(ctx context.Context, tx pgx.Tx, live string, kept *backup.KeepAccessResult) error {
	fenceNamespaces := "UPDATE " + pgIdent(live) + ".namespaces SET failover_version = failover_version + 1"

	if _, err := tx.Exec(ctx, fenceNamespaces); err != nil {
		return fmt.Errorf("fence namespaces after restore: %w", err)
	}

	// The client picks the lock ID of a workspace, so the ID alone cannot tell
	// a lock from before the restore from a lock after it. The restore
	// releases every workspace lock, and a client from before the restore
	// takes a new lock.
	releaseLocks := "UPDATE " + pgIdent(live) + ".workspaces " +
		"SET lock_id = '', lock_holder = '', lock_reason = '', locked_at_ms = 0 WHERE lock_id <> ''"

	if _, err := tx.Exec(ctx, releaseLocks); err != nil {
		return fmt.Errorf("release workspace locks after restore: %w", err)
	}

	clearInventory := "UPDATE " + pgIdent(live) + ".inventory " +
		"SET lock_holder = NULL, lock_since_ms = NULL, lock_operation = NULL WHERE tool = $1"

	if _, err := tx.Exec(ctx, clearInventory, inventoryToolOpenTofu); err != nil {
		return fmt.Errorf("clear workspace inventory locks after restore: %w", err)
	}

	// The backup holds the lease columns of an update that ran when the backup
	// was made. The restore expires the lease of every running update, so the
	// CLI that holds the lease token from before the restore cannot save a
	// checkpoint, renew its lease or complete the update. The update stays
	// running and keeps the stack locked: an operator ends it with a cancel,
	// which builds the journal as for any update. The revision moves with the
	// change of the row.
	expireLeases := "UPDATE " + pgIdent(live) + ".updates " +
		"SET lease_expires_at_ms = 0, revision = revision + 1 WHERE status = $1"

	tag, err := tx.Exec(ctx, expireLeases, string(stack.StatusRunning))
	if err != nil {
		return fmt.Errorf("expire update leases after restore: %w", err)
	}

	kept.ExpiredUpdateLeases = tag.RowsAffected()

	// The restored log can reuse sequences that a sink already holds, and
	// a restored cursor can point past the restored entries. A reset makes
	// every sink receive the restored log again from its first entry. A
	// restored lease could block that reship, so the reset releases every
	// lease too.
	if _, err := tx.Exec(ctx, "DELETE FROM "+pgIdent(live)+".audit_sink_cursors"); err != nil {
		return fmt.Errorf("reset audit sink cursors after restore: %w", err)
	}

	if _, err := tx.Exec(ctx, "DELETE FROM "+pgIdent(live)+".audit_sink_leases"); err != nil {
		return fmt.Errorf("reset audit sink leases after restore: %w", err)
	}

	return nil
}

// keepControlTables replaces the rows of the durable control tables in the
// restored schema live with the rows of the schema previous, in tx, and
// records the runner credentials that the copy does not keep, in the audit
// scope @server through srv, the locked server audit transaction of tx. A restore
// keeps the control tables of the live installation: the claim tokens of
// durable operations only grow, and an older token from a backup would let a
// stale holder write. The installation, backups and restores control tables
// live in the control schema, which the swap never renames, so they stay
// with no copy. The lock holds every durable operation write of another
// instance out until the swap commits, so the copy has the last committed
// rows.
func keepControlTables(ctx context.Context, tx pgx.Tx, srv serverAuditTx, live, previous string) error {
	kept, err := keptControlTables()
	if err != nil {
		return err
	}

	locks := make([]string, 0, len(kept))
	for _, spec := range kept {
		locks = append(locks, pgIdent(previous)+"."+spec.table)
	}

	// The lock holds every durable operation write of another instance out
	// until the swap commits, so the copy has the last committed rows.
	lock := "LOCK TABLE " + strings.Join(locks, ", ") + " IN EXCLUSIVE MODE"

	if _, lockErr := tx.Exec(ctx, lock); lockErr != nil {
		return fmt.Errorf("keep control tables: %w", lockErr)
	}

	// The copy drops the live handoff tokens and job sessions whose runner
	// the restored schema does not hold. Read them under the lock, before
	// the copy: the copy removes them, and this read is the only record of
	// them.
	dropped, err := droppedRunnerCredentials(ctx, tx, live, previous)
	if err != nil {
		return err
	}

	stmts := make([]string, 0, len(kept))

	// The deletes run children first, the copies parents first: backup.ControlTables
	// lists the parents first.
	for _, spec := range slices.Backward(kept) {
		stmts = append(stmts, "DELETE FROM "+pgIdent(live)+"."+spec.table)
	}

	for _, spec := range kept {
		columns := strings.Join(spec.columns, ", ")

		stmts = append(stmts, "INSERT INTO "+pgIdent(live)+"."+spec.table+" ("+columns+") SELECT "+
			columns+" FROM "+keepFrom(live, previous, spec))
	}

	for _, stmt := range stmts {
		if _, execErr := tx.Exec(ctx, stmt); execErr != nil {
			return fmt.Errorf("keep control tables: %w", execErr)
		}
	}

	return appendDroppedRunnerCredentials(ctx, srv, dropped)
}

// droppedRunnerCredentials returns the live handoff tokens and job sessions
// whose runner the restored schema live does not hold. It reads them in tx,
// after the lock of the control tables and before the copy replaces their
// rows. The runner IDs come back sorted and with no repetition.
func droppedRunnerCredentials(
	ctx context.Context, tx pgx.Tx, live, previous string,
) (backup.DroppedRunnerCredentials, error) {
	dropped := " WHERE NOT EXISTS (SELECT 1 FROM " + pgIdent(live) + ".runners r WHERE r.id = t.runner_id)"

	tokens := "SELECT t.runner_id FROM " + pgIdent(previous) + ".runner_handoff_tokens t" + dropped
	sessions := "SELECT t.runner_id FROM " + pgIdent(previous) + ".runner_sessions t" + dropped

	tokenRunners, err := runnerIDsOf(ctx, tx, tokens)
	if err != nil {
		return backup.DroppedRunnerCredentials{}, err
	}

	sessionRunners, err := runnerIDsOf(ctx, tx, sessions)
	if err != nil {
		return backup.DroppedRunnerCredentials{}, err
	}

	return backup.NewDroppedRunnerCredentials(
		int64(len(tokenRunners)), int64(len(sessionRunners)), append(tokenRunners, sessionRunners...),
	), nil
}

// runnerIDsOf runs query in tx and returns the runner_id of every row.
func runnerIDsOf(ctx context.Context, tx pgx.Tx, query string) ([]string, error) {
	rows, err := tx.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("read dropped runner credentials: %w", err)
	}

	defer rows.Close()

	var out []string

	for rows.Next() {
		var id string

		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("read dropped runner credentials: %w", err)
		}

		out = append(out, id)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read dropped runner credentials: %w", err)
	}

	return out, nil
}

// appendDroppedRunnerCredentials appends the audit entry of the dropped
// runner credentials in srv, the locked server audit transaction of the
// swap, after the copy replaced the rows of the restored schema. The entry is
// of the scope @server, so it goes through appendServerEntry. It writes no
// entry when the copy dropped nothing. The entry follows the chain state that
// the swap kept.
func appendDroppedRunnerCredentials(
	ctx context.Context,
	srv serverAuditTx,
	dropped backup.DroppedRunnerCredentials,
) error {
	if dropped.Empty() {
		return nil
	}

	nowMs, err := storeNow(ctx, srv.q)
	if err != nil {
		return err
	}

	if _, err := appendServerEntry(ctx, srv, dropped.Entry(codec.FromMillis(nowMs))); err != nil {
		return fmt.Errorf("append dropped runner credentials entry: %w", err)
	}

	return nil
}

// keepFrom returns the FROM item of the copy of the control table spec from
// the schema previous into the restored schema live, with its WHERE clause. A
// handoff token and a job session name the runner of the claim of a live
// durable operation: the restored data can predate that runner, so the copy
// keeps the credential only while the restored schema holds its runner. A
// credential of a runner that the restored schema does not hold names no
// runner, and a later use must fail as unknown, never with a store error.
func keepFrom(live, previous string, spec pgCopyTable) string {
	from := pgIdent(previous) + "." + spec.table

	if spec.table == "runner_handoff_tokens" || spec.table == "runner_sessions" {
		return from + " t WHERE EXISTS (SELECT 1 FROM " + pgIdent(live) + ".runners r WHERE r.id = t.runner_id)"
	}

	return from
}

// errNoColumnList reports a control table with no column list in the adapter.
var errNoColumnList = errors.New("control table has no column list")

// keptControlTables returns the column list of each table of
// backup.ControlTables that lives in the swapped schema, in the order of
// backup.ControlTables. The list is the same one that the SQLite adapter
// keeps, so a new control table cannot reach one dialect only. A control
// table with no column list is an error: the restore fails closed and does
// not drop the live rows of the table.
func keptControlTables() ([]pgCopyTable, error) {
	specs := make(map[string]pgCopyTable)
	for _, spec := range pgControlTables() {
		specs[spec.table] = spec
	}

	kept := make([]pgCopyTable, 0, len(backup.ControlTables))

	for _, table := range backup.ControlTables {
		if slices.Contains(controlSchemaTables, table) {
			continue
		}

		spec, ok := specs[table]
		if !ok {
			return nil, fmt.Errorf("keep control tables: %w: %s", errNoColumnList, table)
		}

		kept = append(kept, spec)
	}

	return kept, nil
}

// controlSchemaTables lists the tables of backup.ControlTables that live in
// the control schema. The swap never renames that schema, so the live rows
// stay with no copy.
//
//nolint:gochecknoglobals // A fixed table list, as backup.ControlTables is.
var controlSchemaTables = []string{
	backup.TableBackups,
	backup.TableRestores,
	backup.TableInstallation,
	backup.TableMaintenanceRevocations,
	backup.TableInstanceVersions,
	backup.TableStoreChecks,
}

// keepChainState replaces the audit chain state of the restored schema live
// with the row of the schema previous, in tx. The chain state is bookkeeping
// of this installation: a restore keeps the row of the target. The swap
// replaces the log, so the copy sets log_replaced: the server compares the
// restored log with the state, also after a crash. The copy keeps no
// migration: a new approval starts it again. The copy keeps
// entered_maintenance false: the chain enters maintenance mode again when it
// still waits. Every schema seeds the row, so a previous schema without it
// is audit.ErrChainStateMissing.
func keepChainState(ctx context.Context, tx pgx.Tx, live, previous string) error {
	if _, err := tx.Exec(ctx, "DELETE FROM "+pgIdent(live)+".audit_chain_state"); err != nil {
		return fmt.Errorf("keep audit chain state: %w", err)
	}

	const columns = "id, state, changed_at_ms, changed_by, pending, entered_maintenance, log_replaced, migrating_at_ms"

	keep := "INSERT INTO " + pgIdent(live) + ".audit_chain_state (" + columns + ")" +
		" SELECT id, state, changed_at_ms, changed_by, pending, false, true, 0 FROM " +
		pgIdent(previous) + ".audit_chain_state"

	tag, err := tx.Exec(ctx, keep)
	if err != nil {
		return fmt.Errorf("keep audit chain state: %w", err)
	}

	if tag.RowsAffected() == 0 {
		return fmt.Errorf("keep audit chain state: %w", audit.ErrChainStateMissing)
	}

	return nil
}

// hasStagingPrefix reports whether name is a staging schema: the prefix check
// keeps a programming error from swapping a live schema.
func hasStagingPrefix(name string) bool {
	return strings.HasPrefix(name, stagingSchemaPrefix)
}

// BumpFailoverVersions implements backup.Fencer: it increments the failover
// version of every namespace by one (ADR 38 decision 7) and returns the count
// of namespaces bumped, so leases and locks from before the restore stop
// working.
func (s *Store) BumpFailoverVersions(ctx context.Context) (int64, error) {
	n, err := s.queries.BumpFailoverVersions(ctx)
	if err != nil {
		return 0, fmt.Errorf("bump failover versions: %w", err)
	}

	return n, nil
}

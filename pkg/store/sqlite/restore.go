package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/internal/jobstore"
	"opendustpan.org/opendustpan/pkg/store/sqlite/internal/db"
)

var (
	_ backup.RestoreOperations    = (*Store)(nil)
	_ backup.RestoreStaging       = (*Store)(nil)
	_ backup.Applier              = (*Store)(nil)
	_ backup.AccessKeepingApplier = (*Store)(nil)
	_ backup.Fencer               = (*Store)(nil)
)

// GetRestore implements backup.RestoreOperations.
func (s *Store) GetRestore(ctx context.Context, id backup.RestoreID) (backup.RestoreRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.fetchRestore(ctx, id)
}

// fetchRestore reads one restore row. The caller holds the store lock.
func (s *Store) fetchRestore(ctx context.Context, id backup.RestoreID) (backup.RestoreRecord, error) {
	row, err := s.queries.GetRestore(ctx, id.String())
	if errors.Is(err, sql.ErrNoRows) {
		return backup.RestoreRecord{}, backup.ErrRestoreNotFound
	}

	if err != nil {
		return backup.RestoreRecord{}, fmt.Errorf("select restore: %w", err)
	}

	return restoreFromRow(row)
}

// ListRestores implements backup.RestoreOperations: every restore row, newest
// first.
func (s *Store) ListRestores(ctx context.Context) ([]backup.RestoreRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.queries.ListRestores(ctx)
	if err != nil {
		return nil, fmt.Errorf("select restores: %w", err)
	}

	out := make([]backup.RestoreRecord, 0, len(rows))

	for i := range rows {
		r, err := restoreFromRow(db.GetRestoreRow(rows[i]))
		if err != nil {
			return nil, err
		}

		out = append(out, r)
	}

	return out, nil
}

// restoreFromRow converts a restore row into a domain record. Unknown states
// report a corrupt row: every writer validates the state first.
func restoreFromRow(row db.GetRestoreRow) (backup.RestoreRecord, error) {
	return codec.FromRestoreRow(codec.RestoreRow{
		ID:          row.ID,
		State:       row.State,
		BackupID:    row.BackupID,
		PreBackupID: row.PreBackupID,
		RequestedBy: row.RequestedBy,
		Error:       row.Error,
		StartedAtMs: row.StartedAtMs,
		EndedAtMs:   row.EndedAtMs,
		DevReport:   row.DevReport,
	})
}

// ApplyStaging implements backup.Applier: it fences the staged file, copies
// the control tables of the live database into it, and swaps it live with
// one file rename. The staged file passed every check in StagePortable, so
// the rename cannot mix two installations. With no staged file it reports
// ErrCorrupt and changes nothing. The fence transaction holds the store lock
// exclusive, so concurrent readers wait out the copy and the rename instead
// of reading a closed handle, and a durable claim cannot land between the
// copy and the swap. Fencing the staged image before the rename means the
// atomic rename carries fenced versions: a crash after the rename cannot
// leave restored data with old failover versions. The staging path clears
// only after a successful swap, so a failed apply keeps the staged file for
// DiscardStaging.
func (s *Store) ApplyStaging(ctx context.Context) error {
	s.stagingMu.Lock()
	defer s.stagingMu.Unlock()

	if s.stagingPath == "" {
		return backup.ErrCorrupt
	}

	plan := swapPlan{staged: s.stagingPath, keepAccess: false, before: nil, after: nil}
	if _, err := s.swapStaged(ctx, plan); err != nil {
		return err
	}

	s.stagingPath = ""

	return nil
}

// swapPlan is one swap of a staged file into the live database.
type swapPlan struct {
	// staged is the path of the staged file.
	staged string
	// keepAccess keeps the current rows of the access tables.
	keepAccess bool
	// before runs under the store write lock, before the first change to
	// the staged file: the check of the step fence on the live database.
	before func(ctx context.Context) error
	// after runs in the fence transaction of the staged file, after the copy
	// of the control tables: the record of the step in the staged file, so
	// the rename makes it live with the restored data.
	after func(ctx context.Context, tx *sql.Tx, kept backup.KeepAccessResult) error
}

// swapStaged fences the staged file of plan, copies the control tables of
// the live database into it, and swaps it live with one file rename. With
// plan.keepAccess it keeps the current rows of the access tables first. The
// fence transaction holds the store lock exclusive, so concurrent readers
// wait out the copy and the rename instead of reading a closed handle, and a
// durable claim cannot land between the copy and the swap. Fencing the staged
// image before the rename means the atomic rename carries fenced versions: a
// crash after the rename cannot leave restored data with old failover
// versions. It returns the count of the dropped temp_admins rows of a swap
// that keeps the access tables.
func (s *Store) swapStaged(ctx context.Context, plan swapPlan) (int64, error) {
	// The audit chain record is bookkeeping of this installation: the
	// staged file takes the value of the live database. The control tables
	// stay across the swap with the live rows, so the running restore row
	// stays readable during the swap. Read the chain before the write lock,
	// because the read takes the read lock.
	keep, err := s.liveBookkeeping(ctx)
	if err != nil {
		return 0, err
	}

	// Hold the write lock from the fence through the swap, so a durable
	// claim, a control write or a revoke cannot land between the copy and
	// the rename. Lock order is stagingMu before mu.
	s.mu.Lock()
	defer s.mu.Unlock()

	if plan.before != nil {
		if beforeErr := plan.before(ctx); beforeErr != nil {
			return 0, beforeErr
		}
	}

	// A binary below the minimum of the live database must not swap in a
	// file that it built.
	if minErr := s.checkSwapMinimumLocked(ctx); minErr != nil {
		return 0, minErr
	}

	// The check covers the whole staging area, also the access tables that
	// a swap that keeps them does not take, and runs before any write to the
	// staged file.
	if checkErr := s.checkSwapDevLocked(ctx, plan.staged); checkErr != nil {
		return 0, checkErr
	}

	var dropped int64

	if plan.keepAccess {
		if keepErr := s.keepLiveAccessTables(ctx, plan.staged); keepErr != nil {
			return 0, keepErr
		}

		// The swap takes temp_admins from the backup and keeps the live
		// principals, so a staged row can name a principal that the live
		// principals table does not hold.
		if dropped, err = dropDanglingTempAdmins(ctx, plan.staged); err != nil {
			return 0, err
		}
	}

	var after func(context.Context, *sql.Tx) error

	if plan.after != nil {
		kept := backup.KeepAccessResult{DroppedTempAdmins: dropped}
		after = func(ctx context.Context, tx *sql.Tx) error { return plan.after(ctx, tx, kept) }
	}

	if err := fenceStagedFile(ctx, plan.staged, s.path, keep, after); err != nil {
		return 0, err
	}

	if err := s.swapDatabaseLocked(ctx, plan.staged); err != nil {
		return 0, err
	}

	return dropped, nil
}

// fenceStagedFile fences the staged file at path, copies the control tables
// of the live database at live into it, and writes the bookkeeping row keep
// of the live database into it, in one transaction. The caller holds the
// store write lock, so the copy has the last committed control rows. It
// opens the file in rollback-journal mode, so no write-ahead-log sidecar
// survives the fence to leak beside the renamed database.
func fenceStagedFile(
	ctx context.Context, path, live string, keep bookkeeping, after func(context.Context, *sql.Tx) error,
) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("fence staged namespaces: %w", err)
	}

	conn, err := openFencedStaged(path)
	if err != nil {
		return err
	}

	defer func() {
		if closeErr := conn.Close(); closeErr != nil {
			_ = closeErr
		}
	}()

	if attachErr := attachLiveDatabase(ctx, conn, live); attachErr != nil {
		return attachErr
	}

	return applyStagedFence(ctx, conn, keep, after)
}

// openFencedStaged opens the staged file at path in rollback-journal mode
// with one pooled connection, so ATTACH holds for the fence.
func openFencedStaged(path string) (*sql.DB, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("fence staged namespaces: %w", err)
	}

	query := url.Values{}
	query.Set("_txlock", "immediate")
	query.Add("_pragma", "busy_timeout(10000)")
	query.Add("_pragma", "journal_mode(delete)")
	query.Add("_pragma", "foreign_keys(on)")

	dsn := (&url.URL{Scheme: fileScheme, Path: abs, RawQuery: query.Encode()}).String()

	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("fence staged namespaces: %w", err)
	}

	// ATTACH holds for one connection, so the pool keeps one.
	conn.SetMaxOpenConns(1)

	return conn, nil
}

// attachLiveDatabase attaches the live database at live as live and pings
// the staged connection.
func attachLiveDatabase(ctx context.Context, conn *sql.DB, live string) error {
	liveAbs, err := filepath.Abs(live)
	if err != nil {
		return fmt.Errorf("fence staged namespaces: %w", err)
	}

	if _, execErr := conn.ExecContext(ctx, `ATTACH DATABASE ? AS live`, liveAbs); execErr != nil {
		return fmt.Errorf("fence staged namespaces: attach the live database: %w", execErr)
	}

	if pingErr := conn.PingContext(ctx); pingErr != nil {
		return fmt.Errorf("fence staged namespaces: %w", pingErr)
	}

	return nil
}

// applyStagedFence fences the staged namespaces, writes the bookkeeping row,
// resets the runtime rows, copies the control tables and applies the journaled
// revocations again, in one transaction.
func applyStagedFence(
	ctx context.Context, conn *sql.DB, keep bookkeeping, after func(context.Context, *sql.Tx) error,
) error {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("fence staged namespaces: %w", err)
	}

	const fence = `UPDATE main.namespaces SET failover_version = failover_version + 1`

	if _, execErr := tx.ExecContext(ctx, fence); execErr != nil {
		return errors.Join(fmt.Errorf("fence staged namespaces: %w", execErr), tx.Rollback())
	}

	if err := releaseStagedWorkspaceLocks(ctx, tx); err != nil {
		return errors.Join(err, tx.Rollback())
	}

	if err := writeStagedBookkeeping(ctx, tx, keep); err != nil {
		return errors.Join(err, tx.Rollback())
	}

	if err := resetStagedRuntimeRows(ctx, tx); err != nil {
		return errors.Join(err, tx.Rollback())
	}

	if err := copyControlTables(ctx, tx); err != nil {
		return errors.Join(err, tx.Rollback())
	}

	// The staged access tables come from the backup. Apply again the
	// revocations that ran in this stay in maintenance mode, so the swap does not
	// bring back a session or a token that an operator ended.
	if err := replayRevocations(ctx, tx); err != nil {
		return errors.Join(err, tx.Rollback())
	}

	if err := deriveUnitState(ctx, tx); err != nil {
		return errors.Join(err, tx.Rollback())
	}

	if after != nil {
		if err := after(ctx, tx); err != nil {
			return errors.Join(err, tx.Rollback())
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("fence staged namespaces: %w", err)
	}

	return nil
}

// releaseStagedWorkspaceLocks releases every workspace lock of the staged
// file, in tx, and clears the lock on the inventory entry of each workspace.
// The client picks the lock ID, so the ID alone cannot tell a lock from before
// the restore from a lock after it. A client that holds a lock from before
// the restore must take a new lock and read the restored state first.
func releaseStagedWorkspaceLocks(ctx context.Context, tx *sql.Tx) error {
	const release = `UPDATE main.workspaces
SET lock_id = '', lock_holder = '', lock_reason = '', locked_at_ms = 0
WHERE lock_id <> ''`

	if _, err := tx.ExecContext(ctx, release); err != nil {
		return fmt.Errorf("release staged workspace locks: %w", err)
	}

	const clearInventory = `UPDATE main.inventory
SET lock_holder = NULL, lock_since_ms = NULL, lock_operation = NULL
WHERE tool = ?`

	if _, err := tx.ExecContext(ctx, clearInventory, workspaceInventoryTool); err != nil {
		return fmt.Errorf("clear staged workspace inventory locks: %w", err)
	}

	return nil
}

// resetStagedRuntimeRows clears the audit sink cursors and leases of the
// staged file, in tx: the restored log can reuse sequences that a sink
// already holds, and a restored cursor can point past the restored entries.
// A reset makes every sink receive the restored log again from its first
// entry. A restored lease could block that reship, so the reset releases
// every lease too. The control tables stay across the swap with the live
// rows, so no running backup or restore row needs failing.
func resetStagedRuntimeRows(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM main.audit_sink_cursors`); err != nil {
		return fmt.Errorf("reset staged audit sink cursors: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM main.audit_sink_leases`); err != nil {
		return fmt.Errorf("reset staged audit sink leases: %w", err)
	}

	return nil
}

// unitVersion names the unit of the restored installation, the state version
// that the restore derives for it, and the time that the restore gave the
// version.
type unitVersion struct {
	namespace string
	id        string
	version   string
	atMs      int64
}

// deriveUnitState records the state version of every unit of the staged file
// in unit_state, in tx: a restore derives the version from the restored rows
// and brings no stored version back. A workspace takes the state version that
// its row makes current, a stack the digest of its last checkpoint. Every
// derived version is final: the state that the restore brings is the state of
// the installation.
//
// The write is one statement for the workspaces and one for the stacks, and
// the close of the jobs that wait for a state version reads its units in one
// statement. Neither count grows with the number of units.
func deriveUnitState(ctx context.Context, tx *sql.Tx) error {
	if err := upsertStagedUnitState(ctx, tx); err != nil {
		return err
	}

	awaiting, err := stagedAwaitingUnits(ctx, tx)
	if err != nil {
		return err
	}

	q := db.New(tx)

	for _, unit := range awaiting {
		_, err = jobstore.CloseAwaiting(ctx, stateCloseTx{q: q}, namespace.Name(unit.namespace), jobstore.FinalVersion{
			UnitID: unit.id, VersionID: unit.version, Commit: "", Job: "",
			At: time.UnixMilli(unit.atMs).UTC(),
		}, nil)
		if err != nil {
			return fmt.Errorf("close the jobs that wait for the state version: %w", err)
		}
	}

	return nil
}

// upsertStagedUnitState writes the state version of every unit of the staged
// file in unit_state, in tx, with one statement for the workspaces and one for
// the stacks. The final fields are the version, the origin is empty, and the
// time comes from the clock of the database.
func upsertStagedUnitState(ctx context.Context, tx *sql.Tx) error {
	const (
		now     = "CAST(strftime('%s', 'now') AS INTEGER) * 1000 + CAST(substr(strftime('%f', 'now'), 4, 3) AS INTEGER)"
		columns = "unit_id, namespace_id, version_id, final_version_id, final_commit, final_job, final_at_ms"
		update  = " ON CONFLICT (unit_id) DO UPDATE SET namespace_id = excluded.namespace_id," +
			" version_id = excluded.version_id, final_version_id = excluded.final_version_id," +
			" final_commit = excluded.final_commit, final_job = excluded.final_job," +
			" final_at_ms = excluded.final_at_ms"
	)

	workspaceVersion := "w.current_state_version_id"

	// The WHERE true keeps the parser from reading ON CONFLICT as the ON
	// clause of the join.
	workspaces := "INSERT INTO main.unit_state (" + columns + ")" +
		" SELECT w.id, n.id, " + workspaceVersion + ", " + workspaceVersion + ", '', '', " + now +
		" FROM main.workspaces AS w" +
		" JOIN main.namespaces AS n ON n.name = w.namespace" +
		" WHERE true" + update

	version := "lower(hex(c.digest)) || CASE WHEN c.invalid <> 0 THEN ':invalid' ELSE '' END"

	stacks := "INSERT INTO main.unit_state (" + columns + ")" +
		" SELECT c.stack_id, n.id, " + version + ", " + version + ", '', '', " + now +
		" FROM main.checkpoints AS c" +
		" JOIN main.updates AS u ON u.stack_id = c.stack_id AND u.id = c.update_id" +
		" JOIN main.stacks AS st ON st.id = c.stack_id" +
		" JOIN main.namespaces AS n ON n.name = st.namespace" +
		" WHERE u.version = (SELECT MAX(v.version) FROM main.updates AS v" +
		" JOIN main.checkpoints AS last ON last.stack_id = v.stack_id AND last.update_id = v.id" +
		" WHERE v.stack_id = c.stack_id)" + update

	for _, stmt := range []string{workspaces, stacks} {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("derive unit state: %w", err)
		}
	}

	return nil
}

// stagedAwaitingUnits reads the units of the staged file that have jobs that
// wait for a state version, with the final version and the time that the
// restore gave each unit. It reads them in one statement.
func stagedAwaitingUnits(ctx context.Context, tx *sql.Tx) ([]unitVersion, error) {
	const what = "read staged units that wait for a state version"

	const query = `SELECT DISTINCT n.name, j.unit_id, u.version_id, u.final_at_ms
FROM main.jobs AS j
JOIN main.namespaces AS n ON n.id = j.namespace_id
JOIN main.unit_state AS u ON u.unit_id = j.unit_id
WHERE j.state = 'awaiting-state'
ORDER BY n.name, j.unit_id`

	rows, err := tx.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", what, err)
	}

	out, err := scanAwaitingUnits(rows)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", what, err)
	}

	return out, nil
}

// scanAwaitingUnits reads each row of rows and closes it.
func scanAwaitingUnits(rows *sql.Rows) ([]unitVersion, error) {
	var out []unitVersion

	for rows.Next() {
		var unit unitVersion

		if err := rows.Scan(&unit.namespace, &unit.id, &unit.version, &unit.atMs); err != nil {
			closeErr := rows.Close()

			return nil, errors.Join(err, closeErr)
		}

		out = append(out, unit)
	}

	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}

	return out, nil
}

// writeStagedBookkeeping writes the bookkeeping row keep of the live
// database into the staged file, in tx.
func writeStagedBookkeeping(ctx context.Context, tx *sql.Tx, keep bookkeeping) error {
	// The swap replaces the log, so the copy of the record sets
	// log_replaced: the server compares the restored log with the state,
	// also after a crash. The copy keeps no migration: a new approval
	// starts it again. The copy keeps entered_maintenance false: the chain
	// enters maintenance mode again when it still waits.
	const keepChain = `INSERT INTO main.audit_chain_state (id, state, changed_at_ms, changed_by, pending, entered_maintenance, log_replaced, migrating_at_ms)
    VALUES (1, ?, ?, ?, ?, 0, 1, 0)
    ON CONFLICT (id) DO UPDATE SET state = excluded.state, changed_at_ms = excluded.changed_at_ms,
        changed_by = excluded.changed_by, pending = excluded.pending, entered_maintenance = 0,
        log_replaced = 1, migrating_at_ms = 0`

	chain := keep.chain

	_, err := tx.ExecContext(ctx, keepChain, string(chain.State), codec.Millis(chain.ChangedAt), chain.ChangedBy,
		chain.Pending)
	if err != nil {
		return fmt.Errorf("keep audit chain state: %w", err)
	}

	return nil
}

// BumpFailoverVersions implements backup.Fencer: it increments the failover
// version of every namespace by one (ADR 38 decision 7) and returns the count
// of namespaces bumped, so leases and locks from before the restore stop
// working.
func (s *Store) BumpFailoverVersions(ctx context.Context) (int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	n, err := s.queries.BumpFailoverVersions(ctx)
	if err != nil {
		return 0, fmt.Errorf("bump failover versions: %w", err)
	}

	return n, nil
}

// bookkeeping holds the row of the live database that a swap keeps: the
// audit chain record. The control tables stay across the swap with the live
// rows, so the installation state needs no separate copy.
type bookkeeping struct {
	chain audit.ChainRecord
}

// liveBookkeeping reads the row of the live database that a swap keeps.
func (s *Store) liveBookkeeping(ctx context.Context) (bookkeeping, error) {
	chain, err := s.ChainRecord(ctx)
	if err != nil {
		return bookkeeping{}, err
	}

	return bookkeeping{chain: chain}, nil
}

// durableOperationColumns lists the columns of durable_operations.
const durableOperationColumns = `id, start_key, kind, input, version, state, attempt, expiries, failures, ` +
	`last_error, claim_owner, claim_token, claim_expires_at_ms, cancel_requested, not_before_ms, created_at_ms, ` +
	`updated_at_ms`

// durableScheduleColumns lists every column of durable_schedules, for the
// copy of the control tables.
const durableScheduleColumns = `name, interval_ms, next_run_at_ms, last_claimed_at_ms`

// backupColumns lists the columns of backups, for the copy of the control
// tables.
const backupColumns = `id, state, started_at_ms, ended_at_ms, target, error`

// restoreColumns lists the columns of restores, for the copy of the control
// tables.
const restoreColumns = `id, state, backup_id, pre_backup_id, requested_by, started_at_ms, ended_at_ms, error, dev_report, ` +
	`maintenance_entered, audit_events`

// keepControlStatements replace the rows of each table of
// backup.ControlTables in the staged file (main) with the rows of the live
// database (live): the deletes run children first, the copies parents
// first. A restore keeps the control tables of the live database: the claim
// tokens of durable operations only grow, and the installation, backups and
// restores rows describe this installation, so the running restore stays
// readable during the swap.
//
//nolint:gochecknoglobals // A fixed list of SQL statements: Go has no constant slices.
var keepControlStatements = []string{
	`DELETE FROM main.durable_steps`,
	`DELETE FROM main.durable_operations`,
	`DELETE FROM main.durable_schedules`,
	`DELETE FROM main.sign_in_failures`,
	`DELETE FROM main.audit_chain_check`,
	`DELETE FROM main.audit_chain_mark`,
	`DELETE FROM main.backups`,
	`DELETE FROM main.restores`,
	`DELETE FROM main.installation`,
	`DELETE FROM main.runner_claims`,
	`DELETE FROM main.runner_handoff_tokens`,
	`DELETE FROM main.runner_sessions`,
	`DELETE FROM main.runner_nonces`,
	`DELETE FROM main.maintenance_revocations`,
	`DELETE FROM main.instance_versions`,
	`DELETE FROM main.store_checks`,
	`DELETE FROM main.name_locks`,
	`DELETE FROM main.name_lock_key`,
	`DELETE FROM main.used_tickets`,
	`INSERT INTO main.durable_operations (` + durableOperationColumns + `) ` +
		`SELECT ` + durableOperationColumns + ` FROM live.durable_operations`,
	`INSERT INTO main.durable_steps (operation_id, name, seq, result, done_at_ms) ` +
		`SELECT operation_id, name, seq, result, done_at_ms FROM live.durable_steps`,
	`INSERT INTO main.durable_schedules (` + durableScheduleColumns + `) ` +
		`SELECT ` + durableScheduleColumns + ` FROM live.durable_schedules`,
	`INSERT INTO main.sign_in_failures (` + signInFailureColumns + `) ` +
		`SELECT ` + signInFailureColumns + ` FROM live.sign_in_failures`,
	`INSERT INTO main.audit_chain_check (` + auditChainCheckColumns + `) ` +
		`SELECT ` + auditChainCheckColumns + ` FROM live.audit_chain_check`,
	`INSERT INTO main.audit_chain_mark (` + auditChainMarkColumns + `) ` +
		`SELECT ` + auditChainMarkColumns + ` FROM live.audit_chain_mark`,
	`INSERT INTO main.backups (` + backupColumns + `) ` +
		`SELECT ` + backupColumns + ` FROM live.backups`,
	`INSERT INTO main.restores (` + restoreColumns + `) ` +
		`SELECT ` + restoreColumns + ` FROM live.restores`,
	`INSERT INTO main.installation (id, state, kind) SELECT id, state, kind FROM live.installation`,
	`INSERT INTO main.runner_claims (` + runnerClaimColumns + `) ` +
		`SELECT ` + runnerClaimColumns + ` FROM live.runner_claims`,
	`INSERT INTO main.runner_handoff_tokens (` + runnerHandoffColumns + `) ` +
		`SELECT ` + runnerHandoffColumns + ` FROM live.runner_handoff_tokens`,
	`INSERT INTO main.runner_sessions (` + runnerSessionColumns + `) ` +
		`SELECT ` + runnerSessionColumns + ` FROM live.runner_sessions`,
	`INSERT INTO main.runner_nonces (` + runnerNonceColumns + `) ` +
		`SELECT ` + runnerNonceColumns + ` FROM live.runner_nonces`,
	`INSERT INTO main.maintenance_revocations (` + revocationColumns + `) ` +
		`SELECT ` + revocationColumns + ` FROM live.maintenance_revocations`,
	`INSERT INTO main.instance_versions (id, binary_version, started_at_ms) ` +
		`SELECT id, binary_version, started_at_ms FROM live.instance_versions`,
	`INSERT INTO main.store_checks (id, target, state, requested_by, started_at_ms, ended_at_ms, error, report) ` +
		`SELECT id, target, state, requested_by, started_at_ms, ended_at_ms, error, report FROM live.store_checks`,
	`INSERT INTO main.name_locks (` + nameLockColumns + `) SELECT ` + nameLockColumns + ` FROM live.name_locks`,
	`INSERT INTO main.name_lock_key (id, key) SELECT id, key FROM live.name_lock_key`,
	`INSERT INTO main.used_tickets (provider_id, ticket_digest, until_ms) ` +
		`SELECT provider_id, ticket_digest, until_ms FROM live.used_tickets`,
}

// nameLockColumns lists every column of name_locks, for the copy of the
// control tables. A restore keeps the live counts and the live key of their
// names, so a restore never ends a lock and never changes the digests of the
// names.
const nameLockColumns = `scope, name, sub, failures, locked_until_ms, level, last_failure_ms, ` +
	`last_lock_end_ms, reset_after_ms`

// The columns of the control tables of the runner protocol, for the copy of
// the control tables. A restore keeps the live handoff tokens and job
// sessions, because they name the claims of the live durable operations.
const (
	runnerClaimColumns   = `operation_id, token, claimed_at_ms, job_id, phase`
	runnerHandoffColumns = `sha256, operation_id, token, runner_id, issued_at_ms, expires_at_ms, used_at_ms`
	runnerSessionColumns = `id, operation_id, token, runner_id, key_public, key_created_at_ms, created_at_ms`
	runnerNonceColumns   = `subject, nonce, expires_at_ms`
)

// revocationColumns lists every column of maintenance_revocations, for the
// copy of the control tables.
const revocationColumns = `id, kind, target, end_reason, actor, object, at_ms`

// signInFailureColumns lists every column of sign_in_failures, for the copy
// of the control tables.
const signInFailureColumns = `source, window_end_ms, count`

// auditChainCheckColumns lists every column of audit_chain_check, for the
// copy of the control tables.
const auditChainCheckColumns = `id, checked_at_ms, invalid`

// auditChainMarkColumns lists every column of audit_chain_mark, for the copy
// of the control tables.
const auditChainMarkColumns = `scope, epoch, sequence, marked_at_ms`

// copyControlTables runs keepControlStatements in tx, which holds the live
// database attached as live.
func copyControlTables(ctx context.Context, tx *sql.Tx) error {
	for _, stmt := range keepControlStatements {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("keep control tables: %w", err)
		}
	}

	return nil
}

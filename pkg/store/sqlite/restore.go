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
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/sqlite/internal/db"
)

var (
	_ backup.RestoreRegistry      = (*Store)(nil)
	_ backup.Applier              = (*Store)(nil)
	_ backup.AccessKeepingApplier = (*Store)(nil)
	_ backup.Fencer               = (*Store)(nil)
)

// CreateRestore implements backup.RestoreRegistry: it stores r as a running
// restore. The row carries no fence: it describes the installation, not one
// namespace.
func (s *Store) CreateRestore(ctx context.Context, r backup.RestoreRecord) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	row := codec.ToRestoreRow(r)

	n, err := s.queries.CreateRestore(ctx, db.CreateRestoreParams{
		ID:          row.ID,
		State:       row.State,
		BackupID:    row.BackupID,
		PreBackupID: row.PreBackupID,
		RequestedBy: row.RequestedBy,
		StartedAtMs: row.StartedAtMs,
		EndedAtMs:   row.EndedAtMs,
		Error:       row.Error,
	})
	if err != nil {
		return fmt.Errorf("insert restore: %w", err)
	}

	if n == 0 {
		return s.createRestoreConflict(ctx, r.ID)
	}

	return nil
}

// GetRestore implements backup.RestoreRegistry.
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

// ListRestores implements backup.RestoreRegistry: every restore row, newest
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
		r, err := restoreFromRow(rows[i])
		if err != nil {
			return nil, err
		}

		out = append(out, r)
	}

	return out, nil
}

// FinishRestore implements backup.RestoreRegistry: it moves the running
// restore id to state with the error text and the end time.
func (s *Store) FinishRestore(
	ctx context.Context, id backup.RestoreID, state backup.RestoreState, errText string, endedAt time.Time,
) (backup.RestoreRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if _, err := s.fetchRestore(ctx, id); err != nil {
		return backup.RestoreRecord{}, err
	}

	if state != backup.RestoreSucceeded && state != backup.RestoreFailed {
		return backup.RestoreRecord{}, backup.ErrRestoreNotRunning
	}

	n, err := s.queries.FinishRestore(ctx, db.FinishRestoreParams{
		State:     string(state),
		Error:     errText,
		EndedAtMs: codec.OptionalMillis(endedAt),
		ID:        id.String(),
	})
	if err != nil {
		return backup.RestoreRecord{}, fmt.Errorf("finish restore: %w", err)
	}

	if n == 0 {
		return backup.RestoreRecord{}, backup.ErrRestoreNotRunning
	}

	return s.fetchRestore(ctx, id)
}

// FailRunningRestores implements backup.RestoreRegistry: it marks every
// running row failed with the error text and the end time. The server calls
// it at start, after a stop during a restore.
func (s *Store) FailRunningRestores(ctx context.Context, endedAt time.Time, errText string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	err := s.queries.FailRunningRestores(ctx, db.FailRunningRestoresParams{
		Error:     errText,
		EndedAtMs: codec.OptionalMillis(endedAt),
	})
	if err != nil {
		return fmt.Errorf("fail running restores: %w", err)
	}

	return nil
}

// MarkRestoreApplying implements backup.RestoreRegistry: it moves the restore
// id from checking to applying and records the backup taken before the apply.
func (s *Store) MarkRestoreApplying(ctx context.Context, id backup.RestoreID, preBackupID backup.ID) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if _, err := s.fetchRestore(ctx, id); err != nil {
		return err
	}

	n, err := s.queries.MarkRestoreApplying(ctx, db.MarkRestoreApplyingParams{
		PreBackupID: preBackupID.String(),
		ID:          id.String(),
	})
	if err != nil {
		return fmt.Errorf("mark restore applying: %w", err)
	}

	if n == 0 {
		return backup.ErrRestoreNotRunning
	}

	return nil
}

// restoreFromRow converts a restore row into a domain record. Unknown states
// report a corrupt row: every writer validates the state first.
func restoreFromRow(row db.Restore) (backup.RestoreRecord, error) {
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

	staged := s.stagingPath

	// The audit chain record is bookkeeping of this installation: the
	// staged file takes the value of the live database. The control tables
	// stay across the swap with the live rows, so the running restore row
	// stays readable during the swap.
	keep, err := s.liveBookkeeping(ctx)
	if err != nil {
		return err
	}

	// Hold the write lock from the fence through the swap, so a durable
	// claim or a control write cannot land between the copy and the rename.
	// Lock order is stagingMu before mu.
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.checkSwapDevLocked(ctx, staged); err != nil {
		return err
	}

	if err := fenceStagedFile(ctx, staged, s.path, keep); err != nil {
		return err
	}

	if err := s.swapDatabaseLocked(ctx, staged); err != nil {
		return err
	}

	s.stagingPath = ""

	return nil
}

// fenceStagedFile fences the staged file at path, copies the control tables
// of the live database at live into it, and writes the bookkeeping row keep
// of the live database into it, in one transaction. The caller holds the
// store write lock, so the copy has the last committed control rows. It
// opens the file in rollback-journal mode, so no write-ahead-log sidecar
// survives the fence to leak beside the renamed database.
func fenceStagedFile(ctx context.Context, path, live string, keep bookkeeping) error {
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

	return applyStagedFence(ctx, conn, keep)
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
// resets the runtime rows and copies the control tables, in one transaction.
func applyStagedFence(ctx context.Context, conn *sql.DB, keep bookkeeping) error {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("fence staged namespaces: %w", err)
	}

	const fence = `UPDATE main.namespaces SET failover_version = failover_version + 1`

	if _, execErr := tx.ExecContext(ctx, fence); execErr != nil {
		return errors.Join(fmt.Errorf("fence staged namespaces: %w", execErr), tx.Rollback())
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

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("fence staged namespaces: %w", err)
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
const restoreColumns = `id, state, backup_id, pre_backup_id, requested_by, started_at_ms, ended_at_ms, error, dev_report`

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
}

// The columns of the control tables of the runner protocol, for the copy of
// the control tables. A restore keeps the live handoff tokens and job
// sessions, because they name the claims of the live durable operations.
const (
	runnerClaimColumns   = `operation_id, token, claimed_at_ms, job_id, phase`
	runnerHandoffColumns = `sha256, operation_id, token, runner_id, issued_at_ms, expires_at_ms, used_at_ms`
	runnerSessionColumns = `id, operation_id, token, runner_id, key_public, key_created_at_ms, created_at_ms`
	runnerNonceColumns   = `subject, nonce, expires_at_ms`
)

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

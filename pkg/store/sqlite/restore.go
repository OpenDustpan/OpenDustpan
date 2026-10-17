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
	"opendustpan.org/opendustpan/pkg/maintenance"
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

	for _, row := range rows {
		r, err := restoreFromRow(row)
		if err != nil {
			return nil, err
		}

		out = append(out, r)
	}

	return out, nil
}

// FinishRestore implements backup.RestoreRegistry: it moves the running
// restore id to state with the error text and the end time.
//
//nolint:dupl // The backup and restore registries mirror by design: separate tables with distinct domain types.
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
	})
}

// ApplyStaging implements backup.Applier: it increments the failover version
// of every namespace inside the staged file (ADR 38 decisions 6 and 7), then
// swaps the staged restore file into the live database with one file rename
// and drops the staging area. The staged file passed every check in
// StagePortable, so the rename cannot mix two installations. With no staged
// file it reports ErrCorrupt and changes nothing. The swap holds the store
// lock exclusive, so concurrent readers wait out the rename instead of
// reading a closed handle. Fencing the staged image before the rename means
// the atomic rename carries fenced versions: a crash after the rename cannot
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

	// The chain state and the installation state are bookkeeping of this
	// installation: the staged file takes the values of the live database.
	keep, err := s.liveBookkeeping(ctx)
	if err != nil {
		return err
	}

	if err := fenceStagedFile(ctx, staged, keep); err != nil {
		return err
	}

	if err := s.swapDatabase(ctx, staged); err != nil {
		return err
	}

	s.stagingPath = ""

	return nil
}

// fenceStagedFile increments the failover version of every namespace in the
// staged file at path, so the rename carries fenced versions, and writes the
// bookkeeping rows keep of the live database into it, in one transaction. It
// opens the file in rollback-journal mode, so no write-ahead-log sidecar
// survives the fence to leak beside the renamed database.
func fenceStagedFile(ctx context.Context, path string, keep bookkeeping) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("fence staged namespaces: %w", err)
	}

	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("fence staged namespaces: %w", err)
	}

	query := url.Values{}
	query.Set("_txlock", "immediate")
	query.Add("_pragma", "busy_timeout(10000)")
	query.Add("_pragma", "journal_mode(delete)")
	query.Add("_pragma", "foreign_keys(on)")

	dsn := (&url.URL{Scheme: fileScheme, Path: abs, RawQuery: query.Encode()}).String()

	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return fmt.Errorf("fence staged namespaces: %w", err)
	}

	defer func() {
		if closeErr := conn.Close(); closeErr != nil {
			_ = closeErr
		}
	}()

	err = conn.PingContext(ctx)
	if err != nil {
		return fmt.Errorf("fence staged namespaces: %w", err)
	}

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("fence staged namespaces: %w", err)
	}

	const fence = `UPDATE namespaces SET failover_version = failover_version + 1`

	_, err = tx.ExecContext(ctx, fence)
	if err != nil {
		return errors.Join(fmt.Errorf("fence staged namespaces: %w", err), tx.Rollback())
	}

	if err := writeStagedBookkeeping(ctx, tx, keep); err != nil {
		return errors.Join(err, tx.Rollback())
	}

	if err := resetStagedRuntimeRows(ctx, tx); err != nil {
		return errors.Join(err, tx.Rollback())
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("fence staged namespaces: %w", err)
	}

	return nil
}

// resetStagedRuntimeRows clears the rows of the staged file that describe
// work of the installation the backup came from, in tx: the audit sink
// cursors and leases, and the running backup and restore rows.
func resetStagedRuntimeRows(ctx context.Context, tx *sql.Tx) error {
	// The restored log can reuse sequences that a sink already holds, and
	// a restored cursor can point past the restored entries. A reset makes
	// every sink receive the restored log again from its first entry. A
	// restored lease could block that reship, so the reset releases every
	// lease too.
	if _, err := tx.ExecContext(ctx, `DELETE FROM audit_sink_cursors`); err != nil {
		return fmt.Errorf("reset staged audit sink cursors: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM audit_sink_leases`); err != nil {
		return fmt.Errorf("reset staged audit sink leases: %w", err)
	}

	// A backup sees its own row as running when it takes its snapshot, so
	// the staged data holds that row as running. No process runs it in
	// this installation, and the one running backup it would hold blocks
	// every later backup.
	const failRunning = `UPDATE backups SET state = 'failed', error = ?, ended_at_ms = ? WHERE state = 'running'`

	end := codec.Millis(time.Now())

	if _, err := tx.ExecContext(ctx, failRunning, backup.RestoredRunningText, end); err != nil {
		return fmt.Errorf("fail staged running backups: %w", err)
	}

	// The same holds for a restore row: the backup that a restore takes
	// before it applies holds that restore as running. The restore that
	// runs now stores its row again after the swap, and the one running
	// restore the data would hold blocks that row.
	const failRestores = `UPDATE restores SET state = 'failed', error = ?, ended_at_ms = ?
    WHERE state IN ('checking', 'applying')`

	if _, err := tx.ExecContext(ctx, failRestores, backup.RestoredRunningRestoreText, end); err != nil {
		return fmt.Errorf("fail staged running restores: %w", err)
	}

	return nil
}

// writeStagedBookkeeping writes the bookkeeping rows keep of the live
// database into the staged file, in tx.
func writeStagedBookkeeping(ctx context.Context, tx *sql.Tx, keep bookkeeping) error {
	// The swap replaces the log, so the copy of the record sets
	// log_replaced: the server compares the restored log with the state,
	// also after a crash. The copy keeps no migration: a new approval
	// starts it again. The copy keeps entered_maintenance false: the chain
	// enters maintenance mode again when it still waits.
	const keepChain = `INSERT INTO audit_chain_state (id, state, changed_at_ms, changed_by, pending, entered_maintenance, log_replaced, migrating_at_ms)
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

	// A restore runs in maintenance mode. The installation row from the
	// backup would end maintenance mode when the rename lands, before the
	// restore has finished, so the staged file takes the live state.
	const keepState = `INSERT INTO installation (id, state) VALUES (1, ?)
    ON CONFLICT (id) DO UPDATE SET state = excluded.state`

	if _, err := tx.ExecContext(ctx, keepState, string(keep.state)); err != nil {
		return fmt.Errorf("keep installation state: %w", err)
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

// bookkeeping holds the rows of the live database that a swap keeps: the
// audit chain record and the installation state.
type bookkeeping struct {
	chain audit.ChainRecord
	state maintenance.State
}

// liveBookkeeping reads the rows of the live database that a swap keeps.
func (s *Store) liveBookkeeping(ctx context.Context) (bookkeeping, error) {
	chain, err := s.ChainRecord(ctx)
	if err != nil {
		return bookkeeping{}, err
	}

	state, err := s.GetState(ctx)
	if err != nil {
		return bookkeeping{}, fmt.Errorf("read installation state: %w", err)
	}

	return bookkeeping{chain: chain, state: state}, nil
}

// keepControlTables replaces the rows of each table of backup.ControlTables
// in the staged file at staged with the rows of the live database at live,
// in one transaction. A restore keeps the control tables of the live
// database: the claim tokens of durable operations only grow, and an older
// token from a backup would let a stale holder write. The staged file and
// the live database have one schema: the stage checks the schema version.
func keepControlTables(ctx context.Context, staged, live string) error {
	abs, err := filepath.Abs(staged)
	if err != nil {
		return fmt.Errorf("keep control tables: %w", err)
	}

	query := url.Values{}
	query.Set("_txlock", "immediate")
	query.Add("_pragma", "busy_timeout(10000)")
	query.Add("_pragma", "journal_mode(delete)")
	query.Add("_pragma", "foreign_keys(on)")

	dsn := (&url.URL{Scheme: fileScheme, Path: abs, RawQuery: query.Encode()}).String()

	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return fmt.Errorf("keep control tables: %w", err)
	}

	// ATTACH holds for one connection, so the pool keeps one.
	conn.SetMaxOpenConns(1)

	defer func() {
		_ = conn.Close() //nolint:errcheck // The commit below reports a failed write; a close error adds nothing.
	}()

	if _, attachErr := conn.ExecContext(ctx, `ATTACH DATABASE ? AS live`, live); attachErr != nil {
		return fmt.Errorf("keep control tables: attach the live database: %w", attachErr)
	}

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("keep control tables: %w", err)
	}

	if err := copyControlTables(ctx, tx); err != nil {
		return errors.Join(err, tx.Rollback())
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("keep control tables: %w", err)
	}

	return nil
}

// durableOperationColumns lists the columns of durable_operations.
const durableOperationColumns = `id, start_key, kind, input, version, state, attempt, expiries, failures, ` +
	`last_error, claim_owner, claim_token, claim_expires_at_ms, cancel_requested, not_before_ms, created_at_ms, ` +
	`updated_at_ms`

// keepControlStatements replace the rows of each table of
// backup.ControlTables in the staged file (main) with the rows of the live
// database (live): the deletes run children first, the copies parents
// first.
//
//nolint:gochecknoglobals // A fixed list of SQL statements: Go has no constant slices.
var keepControlStatements = []string{
	`DELETE FROM main.durable_steps`,
	`DELETE FROM main.durable_operations`,
	`DELETE FROM main.durable_schedules`,
	`DELETE FROM main.sign_in_failures`,
	`INSERT INTO main.durable_operations (` + durableOperationColumns + `) ` +
		`SELECT ` + durableOperationColumns + ` FROM live.durable_operations`,
	`INSERT INTO main.durable_steps (operation_id, name, seq, result, done_at_ms) ` +
		`SELECT operation_id, name, seq, result, done_at_ms FROM live.durable_steps`,
	`INSERT INTO main.durable_schedules (` + durableScheduleColumns + `) ` +
		`SELECT ` + durableScheduleColumns + ` FROM live.durable_schedules`,
	`INSERT INTO main.sign_in_failures (` + signInFailureColumns + `) ` +
		`SELECT ` + signInFailureColumns + ` FROM live.sign_in_failures`,
}

// signInFailureColumns lists every column of sign_in_failures, for the copy
// of the control tables.
const signInFailureColumns = `source, window_end_ms, count`

// durableScheduleColumns lists every column of durable_schedules, for the
// copy of the control tables.
const durableScheduleColumns = `name, interval_ms, next_run_at_ms, last_claimed_at_ms`

// copyControlTables runs keepControlStatements in tx.
func copyControlTables(ctx context.Context, tx *sql.Tx) error {
	for _, stmt := range keepControlStatements {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("keep control tables: %w", err)
		}
	}

	return nil
}

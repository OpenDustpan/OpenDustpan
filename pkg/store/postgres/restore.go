package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
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
	n, err := s.queries.CreateRestore(ctx, db.CreateRestoreParams{
		ID:          r.ID.String(),
		State:       string(r.State),
		BackupID:    r.BackupID.String(),
		PreBackupID: r.PreBackupID.String(),
		RequestedBy: r.RequestedBy,
		StartedAtMs: codec.Millis(r.StartedAt),
		EndedAtMs:   backupEndedAt(r.EndedAt),
		Error:       r.Error,
	})
	if err != nil {
		return fmt.Errorf("insert restore: %w", err)
	}

	if n == 0 {
		return s.createRestoreConflict(ctx, r.ID)
	}

	return nil
}

// GetRestore implements backup.RestoreRegistry: it returns the restore row
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

// ListRestores implements backup.RestoreRegistry: every restore row, newest
// first.
func (s *Store) ListRestores(ctx context.Context) ([]backup.RestoreRecord, error) {
	rows, err := s.queries.ListRestores(ctx)
	if err != nil {
		return nil, fmt.Errorf("select restores: %w", err)
	}

	out := make([]backup.RestoreRecord, 0, len(rows))

	for _, row := range rows {
		r, err := restoreRecord(row)
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
	if state != backup.RestoreSucceeded && state != backup.RestoreFailed {
		return backup.RestoreRecord{}, backup.ErrRestoreNotRunning
	}

	n, err := s.queries.FinishRestore(ctx, db.FinishRestoreParams{
		State:     string(state),
		Error:     errText,
		EndedAtMs: backupEndedAt(endedAt),
		ID:        id.String(),
	})
	if err != nil {
		return backup.RestoreRecord{}, fmt.Errorf("finish restore: %w", err)
	}

	if n == 0 {
		return s.finishRestoreFailure(ctx, id)
	}

	return s.GetRestore(ctx, id)
}

// finishRestoreFailure answers a finish that changed no row: a missing row
// reports ErrRestoreNotFound, a finished row ErrRestoreNotRunning.
func (s *Store) finishRestoreFailure(
	ctx context.Context, id backup.RestoreID,
) (backup.RestoreRecord, error) {
	if _, err := s.GetRestore(ctx, id); err != nil {
		return backup.RestoreRecord{}, err
	}

	return backup.RestoreRecord{}, backup.ErrRestoreNotRunning
}

// FailRunningRestores implements backup.RestoreRegistry: it marks every
// running row failed with the error text and the end time. The server calls
// it at start, after a stop during a restore.
func (s *Store) FailRunningRestores(ctx context.Context, endedAt time.Time, errText string) error {
	params := db.FailRunningRestoresParams{Error: errText, EndedAtMs: backupEndedAt(endedAt)}

	if err := s.queries.FailRunningRestores(ctx, params); err != nil {
		return fmt.Errorf("fail running restores: %w", err)
	}

	return nil
}

// MarkRestoreApplying implements backup.RestoreRegistry: it moves the restore
// id from checking to applying and records the backup taken before the apply.
func (s *Store) MarkRestoreApplying(ctx context.Context, id backup.RestoreID, preBackupID backup.ID) error {
	n, err := s.queries.MarkRestoreApplying(ctx, db.MarkRestoreApplyingParams{
		PreBackupID: preBackupID.String(),
		ID:          id.String(),
	})
	if err != nil {
		return fmt.Errorf("mark restore applying: %w", err)
	}

	if n == 0 {
		return s.markApplyingFailure(ctx, id)
	}

	return nil
}

// markApplyingFailure answers a mark that changed no row: a missing row
// reports ErrRestoreNotFound, a row out of checking ErrRestoreNotRunning.
func (s *Store) markApplyingFailure(ctx context.Context, id backup.RestoreID) error {
	if _, err := s.GetRestore(ctx, id); err != nil {
		return err
	}

	return backup.ErrRestoreNotRunning
}

// restoreRecord converts a restore row into a domain record. A NULL end time
// reads as the zero time: the restore still runs. An unknown state reports a
// corrupt row: every writer validates the state first.
func restoreRecord(row db.Restore) (backup.RestoreRecord, error) {
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

	if err := s.swapStagingSchema(ctx, staging); err != nil {
		return err
	}

	s.stagingSchema = ""

	return nil
}

// swapStagingSchema renames the live schema aside, renames the staging schema
// to the live name, drops the old live schema, and increments the failover
// version of every namespace, all in one transaction (ADR 38 decisions 6
// and 7). A failure rolls back and leaves the live schema in place. The
// staging schema carries its own schema_migrations, so the swapped schema
// keeps its version. Fencing in the same transaction means a crash after the
// swap cannot leave restored data with old failover versions: either both
// land or neither does. The SQLite adapter fences the staged file before its
// rename for the same guarantee: a file rename cannot share a transaction
// with SQL, so it carries already-fenced versions instead.
func (s *Store) swapStagingSchema(ctx context.Context, staging string) error {
	if !hasStagingPrefix(staging) {
		return fmt.Errorf("apply staging schema %q: %w", staging, backup.ErrCorrupt)
	}

	opts := pgx.TxOptions{
		IsoLevel:       pgx.ReadCommitted,
		AccessMode:     pgx.ReadWrite,
		DeferrableMode: pgx.NotDeferrable,
		BeginQuery:     "",
		CommitQuery:    "",
	}

	return inBackupTx(ctx, s.pool, opts, func(tx pgx.Tx) error {
		var live string

		if err := tx.QueryRow(ctx, "SELECT current_schema()").Scan(&live); err != nil {
			return fmt.Errorf("read live schema: %w", err)
		}

		if live == "" || live == staging {
			return fmt.Errorf("apply staging schema %q over %q: %w", staging, live, backup.ErrCorrupt)
		}

		previous := live + "_previous"

		if _, err := tx.Exec(ctx, "ALTER SCHEMA "+pgIdent(live)+" RENAME TO "+pgIdent(previous)); err != nil {
			return fmt.Errorf("move live schema aside: %w", err)
		}

		if _, err := tx.Exec(ctx, "ALTER SCHEMA "+pgIdent(staging)+" RENAME TO "+pgIdent(live)); err != nil {
			return fmt.Errorf("move staged schema live: %w", err)
		}

		return finishSwap(ctx, tx, live, previous)
	})
}

// finishSwap ends the swap in tx: it keeps the audit chain state and the
// installation state of this installation, drops the schema previous, and
// fences the restored schema live (see fenceRestored).
func finishSwap(ctx context.Context, tx pgx.Tx, live, previous string) error {
	if err := keepChainState(ctx, tx, live, previous); err != nil {
		return err
	}

	if err := keepInstallationState(ctx, tx, live, previous); err != nil {
		return err
	}

	if err := keepControlTables(ctx, tx, live, previous); err != nil {
		return err
	}

	if _, err := tx.Exec(ctx, "DROP SCHEMA "+pgIdent(previous)+" CASCADE"); err != nil {
		return fmt.Errorf("drop previous schema: %w", err)
	}

	return fenceRestored(ctx, tx, live)
}

// fenceRestored increments the failover version of every namespace of the
// restored schema live, resets the audit sink cursors and leases, and fails
// the running backup and restore rows of the restored data, in tx.
func fenceRestored(ctx context.Context, tx pgx.Tx, live string) error {
	fenceNamespaces := "UPDATE " + pgIdent(live) + ".namespaces SET failover_version = failover_version + 1"

	if _, err := tx.Exec(ctx, fenceNamespaces); err != nil {
		return fmt.Errorf("fence namespaces after restore: %w", err)
	}

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

	// A backup sees its own row as running when it takes its snapshot, so
	// the restored data holds that row as running. No process runs it in
	// this installation, and the one running backup it would hold blocks
	// every later backup.
	failRunning := "UPDATE " + pgIdent(live) + ".backups SET state = 'failed', error = $1," +
		" ended_at_ms = (extract(epoch from clock_timestamp()) * 1000)::bigint WHERE state = 'running'"

	if _, err := tx.Exec(ctx, failRunning, backup.RestoredRunningText); err != nil {
		return fmt.Errorf("fail restored running backups: %w", err)
	}

	// The same holds for a restore row: the backup that a restore takes
	// before it applies holds that restore as running. The restore that
	// runs now stores its row again after the swap, and the one running
	// restore the data would hold blocks that row.
	failRestores := "UPDATE " + pgIdent(live) + ".restores SET state = 'failed', error = $1," +
		" ended_at_ms = (extract(epoch from clock_timestamp()) * 1000)::bigint" +
		" WHERE state IN ('checking', 'applying')"

	if _, err := tx.Exec(ctx, failRestores, backup.RestoredRunningRestoreText); err != nil {
		return fmt.Errorf("fail restored running restores: %w", err)
	}

	return nil
}

// keepInstallationState replaces the installation row of the restored schema
// live with the row of the schema previous, in tx. The installation state is
// bookkeeping of this installation: a restore runs in maintenance mode, and
// the row from the backup would end maintenance mode when the swap commits,
// before the restore has finished. The migration seeds the row, so previous
// holds it: without a row in previous, the copy reports
// maintenance.ErrUnavailable and the swap fails, so live never lacks the row.
func keepInstallationState(ctx context.Context, tx pgx.Tx, live, previous string) error {
	if _, err := tx.Exec(ctx, "DELETE FROM "+pgIdent(live)+".installation"); err != nil {
		return fmt.Errorf("keep installation state: %w", err)
	}

	keep := "INSERT INTO " + pgIdent(live) + ".installation (id, state) SELECT id, state FROM " +
		pgIdent(previous) + ".installation"

	tag, err := tx.Exec(ctx, keep)
	if err != nil {
		return fmt.Errorf("keep installation state: %w", err)
	}

	if tag.RowsAffected() == 0 {
		return fmt.Errorf("keep installation state: row is missing: %w", maintenance.ErrUnavailable)
	}

	return nil
}

// durableOperationColumns lists the columns of durable_operations.
const durableOperationColumns = "id, start_key, kind, input, version, state, attempt, expiries, failures, " +
	"last_error, claim_owner, claim_token, claim_expires_at_ms, cancel_requested, not_before_ms, created_at_ms, " +
	"updated_at_ms"

// durableScheduleColumns lists every column of durable_schedules, for the
// copy of the control tables.
const durableScheduleColumns = "name, interval_ms, next_run_at_ms, last_claimed_at_ms"

// keepControlTables replaces the rows of each table of backup.ControlTables
// in the restored schema live with the rows of the schema previous, in tx. A
// restore keeps the control tables of the live installation: the claim
// tokens of durable operations only grow, and an older token from a backup
// would let a stale holder write. The lock holds every durable operation
// write of another instance out until the swap commits, so the copy has the
// last committed rows.
func keepControlTables(ctx context.Context, tx pgx.Tx, live, previous string) error {
	stmts := []string{
		"LOCK TABLE " + pgIdent(previous) + ".durable_operations, " + pgIdent(previous) +
			".durable_steps, " + pgIdent(previous) + ".durable_schedules IN EXCLUSIVE MODE",
		"DELETE FROM " + pgIdent(live) + ".durable_steps",
		"DELETE FROM " + pgIdent(live) + ".durable_operations",
		"DELETE FROM " + pgIdent(live) + ".durable_schedules",
		"INSERT INTO " + pgIdent(live) + ".durable_operations (" + durableOperationColumns + ") SELECT " +
			durableOperationColumns + " FROM " + pgIdent(previous) + ".durable_operations",
		"INSERT INTO " + pgIdent(live) + ".durable_steps (operation_id, name, seq, result, done_at_ms)" +
			" SELECT operation_id, name, seq, result, done_at_ms FROM " + pgIdent(previous) + ".durable_steps",
		"INSERT INTO " + pgIdent(live) + ".durable_schedules (" + durableScheduleColumns + ") SELECT " +
			durableScheduleColumns + " FROM " + pgIdent(previous) + ".durable_schedules",
	}

	for _, stmt := range stmts {
		if _, err := tx.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("keep control tables: %w", err)
		}
	}

	return nil
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

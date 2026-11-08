package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/sqlite/internal/db"
)

// areaInfix joins the name of the live database file and the name of a
// staging area: the area of restore rst-<id> and token <token> is the file
// <database>.stage-rst-<id>-<token> next to the live database.
const areaInfix = ".stage-"

// areaSuffixPattern matches the part of a staging file name after the name
// of the live database file and areaInfix.
var areaSuffixPattern = regexp.MustCompile(`^(rst-[a-z2-7]{20})-([1-9]\d{0,18})$`)

// areaPath returns the path of the staging file of the area a. It checks a
// again, so a value that the service did not build never reaches a path.
func (s *Store) areaPath(a backup.StagingArea) (string, error) {
	// Defense in depth: the service builds each area with NewStagingArea;
	// this check stays so that no caller of the port can put another value
	// into a path. The containment check below does not rely on it.
	checked, err := backup.NewStagingArea(a.Restore, a.Token)
	if err != nil {
		return "", err
	}

	dir := filepath.Dir(s.path)
	name := filepath.Base(s.path) + areaInfix + checked.Restore.String() + "-" + strconv.FormatInt(checked.Token, 10)
	path := filepath.Join(dir, name)

	// Defense in depth: the name holds no separator, so the path stays in
	// the directory of the live database. This check stays so that a change
	// of the name rule cannot leave that directory.
	rel, err := filepath.Rel(dir, path)
	if err != nil || rel != name || strings.ContainsRune(rel, filepath.Separator) {
		return "", fmt.Errorf("staging area %s: %w", a, backup.ErrInvalidRestoreID)
	}

	return path, nil
}

// ownsRestore returns durable.ErrClaimLost unless the operation of f is the
// operation that runs restore id.
func ownsRestore(ctx context.Context, q *db.Queries, f durable.StepFence, id backup.RestoreID) error {
	op, err := q.GetDurableOperation(ctx, string(f.Operation))
	if err != nil {
		return fmt.Errorf("select operation of the fence: %w", err)
	}

	if op.Kind != string(backup.RestoreOperationKind) || op.StartKey != backup.RestoreOperationKey(id) {
		return durable.ErrClaimLost
	}

	return nil
}

// fencedRestore checks f, and that its operation runs restore id, in the
// transaction of q. It returns the row of the operation and the time of the
// store clock.
func fencedRestore(
	ctx context.Context, q *db.Queries, f durable.StepFence, id backup.RestoreID,
) (db.DurableOperation, int64, error) {
	row, now, err := checkStep(ctx, q, f)
	if err != nil {
		return db.DurableOperation{}, 0, err
	}

	// Defense in depth: the fence checked above is live, but it must belong
	// to the operation of this restore. The service passes the right fence;
	// this check stays so that no caller can write the row of one restore
	// under the claim of another operation.
	if ownErr := ownsRestore(ctx, q, f, id); ownErr != nil {
		return db.DurableOperation{}, 0, ownErr
	}

	return row, now, nil
}

// recordedRestore checks f as fencedRestore does, and records the step of f
// with no result, in the transaction of q.
func recordedRestore(ctx context.Context, q *db.Queries, f durable.StepFence, id backup.RestoreID) error {
	row, now, err := fencedRestore(ctx, q, f, id)
	if err != nil {
		return err
	}

	return insertStep(ctx, q, stepAt{row: row, now: now}, f, nil)
}

// recordedRestoreWith checks f as fencedRestore does, and records the step of
// f with result, in the transaction of q.
func recordedRestoreWith(
	ctx context.Context, q *db.Queries, f durable.StepFence, id backup.RestoreID, result []byte,
) error {
	row, now, err := fencedRestore(ctx, q, f, id)
	if err != nil {
		return err
	}

	return insertStep(ctx, q, stepAt{row: row, now: now}, f, result)
}

// runningRestore returns the state and the maintenance flag of the running
// restore id in the transaction of q: ErrRestoreNotFound when it does not
// exist, and ErrRestoreNotRunning when it ended.
func runningRestore(ctx context.Context, q *db.Queries, id backup.RestoreID) (db.GetRestoreMaintenanceRow, error) {
	row, err := q.GetRestoreMaintenance(ctx, id.String())
	if errors.Is(err, sql.ErrNoRows) {
		return db.GetRestoreMaintenanceRow{}, backup.ErrRestoreNotFound
	}

	if err != nil {
		return db.GetRestoreMaintenanceRow{}, fmt.Errorf("select restore: %w", err)
	}

	if row.State != string(backup.RestoreChecking) && row.State != string(backup.RestoreApplying) {
		return db.GetRestoreMaintenanceRow{}, backup.ErrRestoreNotRunning
	}

	return row, nil
}

// StartRestore implements backup.RestoreOperations.
func (s *Store) StartRestore(ctx context.Context, r backup.RestoreRecord, start durable.StartRequest) error {
	report, err := restoreStartReport(r, start)
	if err != nil {
		return err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.inControlTx(ctx, func(q *db.Queries) error {
		return insertRestoreWithOperation(ctx, q, r, start, report)
	})
}

// restoreStartReport checks the start of restore r with its operation start,
// and returns the encoded dev report of r.
func restoreStartReport(r backup.RestoreRecord, start durable.StartRequest) (string, error) {
	if err := start.Validate(); err != nil {
		return "", fmt.Errorf("start restore %s: %w", r.ID, err)
	}

	if _, err := backup.ParseRestoreID(r.ID.String()); err != nil {
		return "", fmt.Errorf("start restore: %w", err)
	}

	// Defense in depth: the service builds the request from the restore ID.
	// This check stays so that no caller can start the row of one restore
	// with the operation of another kind or key.
	if start.Key != backup.RestoreOperationKey(r.ID) || start.Kind != backup.RestoreOperationKind {
		return "", fmt.Errorf("start restore %s: operation %q of kind %q: %w",
			r.ID, start.Key, start.Kind, durable.ErrKeyConflict)
	}

	if r.DevReport == nil {
		return "", nil
	}

	return codec.EncodeDevReport(*r.DevReport)
}

// insertRestoreWithOperation inserts the row of restore r and its operation
// start in the transaction of q.
func insertRestoreWithOperation(
	ctx context.Context, q *db.Queries, r backup.RestoreRecord, start durable.StartRequest, report string,
) error {
	n, err := q.InsertRestore(ctx, db.InsertRestoreParams{
		ID:          r.ID.String(),
		BackupID:    r.BackupID.String(),
		RequestedBy: r.RequestedBy,
		StartedAtMs: codec.Millis(r.StartedAt),
		DevReport:   report,
	})
	if err != nil {
		return fmt.Errorf("insert restore: %w", err)
	}

	if n == 0 {
		if _, getErr := q.GetRestoreMaintenance(ctx, r.ID.String()); getErr == nil {
			return backup.ErrRestoreExists
		}

		return backup.ErrRestoreRunning
	}

	_, created, err := startDurable(ctx, q, start)
	if err != nil {
		return err
	}

	if !created {
		// A new row needs a new operation: a taken key is a start of
		// another restore.
		return fmt.Errorf("start restore operation %q: %w", start.Key, durable.ErrKeyConflict)
	}

	return nil
}

// RecordRestoreReport implements backup.RestoreOperations.
func (s *Store) RecordRestoreReport(
	ctx context.Context, f durable.StepFence, id backup.RestoreID, report backup.DevStripReport,
) error {
	text, err := codec.EncodeDevReport(report)
	if err != nil {
		return err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.inControlTx(ctx, func(q *db.Queries) error {
		if err := recordedRestore(ctx, q, f, id); err != nil {
			return err
		}

		if _, err := runningRestore(ctx, q, id); err != nil {
			return err
		}

		params := db.SetRestoreDevReportParams{DevReport: text, ID: id.String()}
		if _, err := q.SetRestoreDevReport(ctx, params); err != nil {
			return fmt.Errorf("store restore dev report: %w", err)
		}

		return nil
	})
}

// SetRestoreMaintenance implements backup.RestoreOperations.
func (s *Store) SetRestoreMaintenance(
	ctx context.Context, f durable.StepFence, id backup.RestoreID, expected, next maintenance.State,
) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.setStateIfWithin(ctx, expected, next, func(q *db.Queries) error {
		if _, _, err := fencedRestore(ctx, q, f, id); err != nil {
			return err
		}

		if _, err := runningRestore(ctx, q, id); err != nil {
			return err
		}

		flag := int64(0)
		if next == maintenance.StateMaintenance {
			flag = 1
		}

		params := db.SetRestoreMaintenanceEnteredParams{MaintenanceEntered: flag, ID: id.String()}
		if _, err := q.SetRestoreMaintenanceEntered(ctx, params); err != nil {
			return fmt.Errorf("store the maintenance flag of restore %s: %w", id, err)
		}

		return nil
	})
}

// ConfirmRestoreMaintenance implements backup.RestoreOperations.
func (s *Store) ConfirmRestoreMaintenance(
	ctx context.Context, f durable.StepFence, id backup.RestoreID, want maintenance.State, canceled []string,
) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var entered bool

	err := s.inControlTx(ctx, func(q *db.Queries) error {
		row, now, err := fencedRestore(ctx, q, f, id)
		if err != nil {
			return err
		}

		restore, err := runningRestore(ctx, q, id)
		if err != nil {
			return err
		}

		state, err := q.GetInstallationState(ctx)
		if err != nil {
			return fmt.Errorf("read installation state sqlite database: %w", maintenance.ErrUnavailable)
		}

		if maintenance.State(state) != want {
			return fmt.Errorf("installation state is not %q: %w", want, maintenance.ErrStateChanged)
		}

		entered = restore.MaintenanceEntered != 0

		result, err := backup.EncodeRestoreMaintenance(backup.RestoreMaintenanceResult{
			Entered: entered, Canceled: canceled, Token: f.Token,
		})
		if err != nil {
			return err
		}

		return insertStep(ctx, q, stepAt{row: row, now: now}, f, result)
	})
	if err != nil {
		return false, err
	}

	return entered, nil
}

// MarkRestoreApplying implements backup.RestoreOperations.
func (s *Store) MarkRestoreApplying(
	ctx context.Context, f durable.StepFence, id backup.RestoreID, preBackupID backup.ID, result []byte,
) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.inControlTx(ctx, func(q *db.Queries) error {
		if err := recordedRestoreWith(ctx, q, f, id, result); err != nil {
			return err
		}

		n, err := q.MarkRestoreApplying(ctx, db.MarkRestoreApplyingParams{
			PreBackupID: preBackupID.String(),
			ID:          id.String(),
		})
		if err != nil {
			return fmt.Errorf("mark restore applying: %w", err)
		}

		if n == 0 {
			if _, err := runningRestore(ctx, q, id); err != nil {
				return err
			}

			return backup.ErrRestoreNotRunning
		}

		return nil
	})
}

// RecordRestoreEvent implements backup.RestoreOperations.
func (s *Store) RecordRestoreEvent(
	ctx context.Context, f durable.StepFence, id backup.RestoreID, object string,
) error {
	// Defense in depth: AddRestoreEvent checks the object in the
	// transaction too; this check stays so that no query runs for an object
	// that cannot be stored.
	if err := backup.ValidateRestoreEvent(object); err != nil {
		return err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.inControlTx(ctx, func(q *db.Queries) error {
		if _, _, err := fencedRestore(ctx, q, f, id); err != nil {
			return err
		}

		row, err := q.GetRestoreAuditEvents(ctx, id.String())
		if errors.Is(err, sql.ErrNoRows) {
			return backup.ErrRestoreNotFound
		}

		if err != nil {
			return fmt.Errorf("select restore audit events: %w", err)
		}

		if row.State != string(backup.RestoreChecking) && row.State != string(backup.RestoreApplying) {
			return backup.ErrRestoreNotRunning
		}

		events, changed, err := backup.AddRestoreEvent(row.AuditEvents, object)
		if err != nil || !changed {
			return err
		}

		params := db.SetRestoreAuditEventsParams{AuditEvents: events, ID: id.String()}
		if _, err := q.SetRestoreAuditEvents(ctx, params); err != nil {
			return fmt.Errorf("store restore audit events: %w", err)
		}

		return nil
	})
}

// CheckRestoreFence implements backup.RestoreOperations.
func (s *Store) CheckRestoreFence(ctx context.Context, f durable.StepFence, id backup.RestoreID) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.inControlTx(ctx, func(q *db.Queries) error {
		if _, _, err := fencedRestore(ctx, q, f, id); err != nil {
			return err
		}

		_, err := runningRestore(ctx, q, id)

		return err
	})
}

// RestoreEvents implements backup.RestoreOperations.
func (s *Store) RestoreEvents(ctx context.Context, id backup.RestoreID) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	row, err := s.queries.GetRestoreAuditEvents(ctx, id.String())
	if errors.Is(err, sql.ErrNoRows) {
		return nil, backup.ErrRestoreNotFound
	}

	if err != nil {
		return nil, fmt.Errorf("select restore audit events: %w", err)
	}

	return backup.DecodeRestoreEvents(row.AuditEvents)
}

// RestoreMaintenanceEntered implements backup.RestoreOperations.
func (s *Store) RestoreMaintenanceEntered(ctx context.Context, id backup.RestoreID) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	row, err := s.queries.GetRestoreMaintenance(ctx, id.String())
	if errors.Is(err, sql.ErrNoRows) {
		return false, backup.ErrRestoreNotFound
	}

	if err != nil {
		return false, fmt.Errorf("select restore: %w", err)
	}

	return row.MaintenanceEntered != 0, nil
}

// AppendRestoreAudit implements backup.RestoreOperations.
func (s *Store) AppendRestoreAudit(
	ctx context.Context, f durable.StepFence, id backup.RestoreID, entries []audit.Entry,
) ([]audit.Entry, error) {
	for i := range entries {
		if err := validServerEntry(entries[i]); err != nil {
			return nil, err
		}
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	var stored []audit.Entry

	err := s.inControlTx(ctx, func(q *db.Queries) error {
		var appendErr error

		stored, appendErr = appendRestoreEntries(ctx, q, f, id, entries)

		return appendErr
	})
	if err != nil {
		return nil, err
	}

	return stored, nil
}

// appendRestoreEntries appends entries to the audit log of the installation
// and records the step of f, in the transaction of q.
func appendRestoreEntries(
	ctx context.Context, q *db.Queries, f durable.StepFence, id backup.RestoreID, entries []audit.Entry,
) ([]audit.Entry, error) {
	if err := recordedRestore(ctx, q, f, id); err != nil {
		return nil, err
	}

	row, err := runningRestore(ctx, q, id)
	if err != nil {
		return nil, err
	}

	if row.State != string(backup.RestoreApplying) {
		return nil, backup.ErrRestoreNotRunning
	}

	stored := make([]audit.Entry, 0, len(entries))

	for i := range entries {
		got, appendErr := appendAuditEntry(ctx, q, entries[i])
		if appendErr != nil {
			return nil, appendErr
		}

		stored = append(stored, got)
	}

	return stored, nil
}

// FinishRestore implements backup.RestoreOperations.
func (s *Store) FinishRestore(
	ctx context.Context, f durable.StepFence, end backup.RestoreEnd,
) (backup.RestoreRecord, error) {
	if end.State != backup.RestoreSucceeded && end.State != backup.RestoreFailed {
		return backup.RestoreRecord{}, backup.ErrRestoreNotRunning
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	var out backup.RestoreRecord

	err := s.inControlTx(ctx, func(q *db.Queries) error {
		if err := recordedRestore(ctx, q, f, end.ID); err != nil {
			return err
		}

		row, err := runningRestore(ctx, q, end.ID)
		if err != nil {
			return err
		}

		if endErr := closeRestore(ctx, q, end, row); endErr != nil {
			return endErr
		}

		got, err := q.GetRestore(ctx, end.ID.String())
		if err != nil {
			return fmt.Errorf("select finished restore: %w", err)
		}

		out, err = restoreFromRow(got)

		return err
	})
	if err != nil {
		return backup.RestoreRecord{}, err
	}

	return out, nil
}

// closeRestore ends the running restore end.ID in the transaction of q. With
// end.LeaveMaintenance, and when row says that the restore entered
// maintenance mode, it leaves maintenance mode first.
func closeRestore(ctx context.Context, q *db.Queries, end backup.RestoreEnd, row db.GetRestoreMaintenanceRow) error {
	if row.MaintenanceEntered != 0 && end.LeaveMaintenance {
		if err := leaveAtRestoreEnd(ctx, q, end.ID); err != nil {
			return err
		}
	}

	return endRestore(ctx, q, end)
}

// leaveAtRestoreEnd moves the installation from maintenance to normal
// operation at the end of restore id, in the transaction of q, with a
// compare-and-set: a state that is already normal stays.
func leaveAtRestoreEnd(ctx context.Context, q *db.Queries, id backup.RestoreID) error {
	params := db.SetInstallationStateIfParams{
		State: string(maintenance.StateNormal), State_2: string(maintenance.StateMaintenance),
	}
	if _, err := q.SetInstallationStateIf(ctx, params); err != nil {
		return fmt.Errorf("leave maintenance mode at the end of restore %s: %w", id, maintenance.ErrUnavailable)
	}

	return nil
}

// endRestore ends the running restore end.ID in the transaction of q.
func endRestore(ctx context.Context, q *db.Queries, end backup.RestoreEnd) error {
	current, err := q.GetRestore(ctx, end.ID.String())
	if err != nil {
		return fmt.Errorf("select restore: %w", err)
	}

	report := current.DevReport

	if end.DevReport != nil {
		if report, err = codec.EncodeDevReport(*end.DevReport); err != nil {
			return err
		}
	}

	n, err := q.EndRestore(ctx, db.EndRestoreParams{
		State:     string(end.State),
		Error:     end.Error,
		EndedAtMs: codec.OptionalMillis(end.EndedAt),
		DevReport: report,
		ID:        end.ID.String(),
	})
	if err != nil {
		return fmt.Errorf("finish restore: %w", err)
	}

	if n == 0 {
		return backup.ErrRestoreNotRunning
	}

	return nil
}

// FailEndedRestores implements backup.RestoreOperations.
func (s *Store) FailEndedRestores(ctx context.Context, endedAt time.Time, leave bool) ([]backup.RestoreRecord, error) {
	rows, err := s.ListRestores(ctx)
	if err != nil {
		return nil, err
	}

	var failed []backup.RestoreRecord

	for i := range rows {
		if rows[i].State != backup.RestoreChecking && rows[i].State != backup.RestoreApplying {
			continue
		}

		record, ended, err := s.failEndedRestore(ctx, rows[i].ID, endedAt, leave)
		if err != nil {
			return failed, err
		}

		if ended {
			failed = append(failed, record)
		}
	}

	return failed, nil
}

// endedRestoreText returns the error text of a restore whose operation is in
// state, and false while the operation has not ended canceled or failed.
func endedRestoreText(id backup.RestoreID, state string) (string, bool, error) {
	switch durable.State(state) {
	case durable.StateCanceled:
		return backup.RestoreCanceledText, true, nil
	case durable.StateFailed:
		return backup.RestoreStoppedText, true, nil
	case durable.StatePending, durable.StateRunning, durable.StateSucceeded:
		return "", false, nil
	default:
		return "", false, fmt.Errorf("durable operation of restore %s has the unknown state %q: %w", id, state,
			errUnknownDurableState)
	}
}

// failEndedRestore marks the running restore id failed when its operation
// ended canceled or failed, in one transaction. It reports whether it did.
func (s *Store) failEndedRestore(
	ctx context.Context, id backup.RestoreID, endedAt time.Time, leave bool,
) (backup.RestoreRecord, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var (
		out   backup.RestoreRecord
		ended bool
	)

	err := s.inControlTx(ctx, func(q *db.Queries) error {
		var failErr error

		end := backup.RestoreEnd{
			ID: id, State: backup.RestoreFailed, Error: "", EndedAt: endedAt, DevReport: nil, LeaveMaintenance: leave,
		}

		out, ended, failErr = failEndedRestoreIn(ctx, q, end)

		return failErr
	})

	return out, ended, err
}

// failEndedRestoreIn is failEndedRestore in the transaction of q, for the
// end of the restore end.ID with the error text of its operation.
func failEndedRestoreIn(ctx context.Context, q *db.Queries, end backup.RestoreEnd) (backup.RestoreRecord, bool, error) {
	var none backup.RestoreRecord

	id := end.ID

	op, err := q.GetDurableOperationByKey(ctx, backup.RestoreOperationKey(id))
	if errors.Is(err, sql.ErrNoRows) {
		return none, false, nil
	}

	if err != nil {
		return none, false, fmt.Errorf("select the operation of restore %s: %w", id, err)
	}

	text, ended, err := endedRestoreText(id, op.State)
	if err != nil || !ended {
		return none, false, err
	}

	row, err := runningRestore(ctx, q, id)
	if errors.Is(err, backup.ErrRestoreNotRunning) || errors.Is(err, backup.ErrRestoreNotFound) {
		return none, false, nil
	}

	if err != nil {
		return none, false, err
	}

	end.Error = text

	if endErr := closeRestore(ctx, q, end, row); endErr != nil {
		return none, false, endErr
	}

	got, err := q.GetRestore(ctx, id.String())
	if err != nil {
		return none, false, fmt.Errorf("select failed restore: %w", err)
	}

	out, err := restoreFromRow(got)
	if err != nil {
		return none, false, err
	}

	return out, true, nil
}

// StageRestore implements backup.RestoreStaging.
func (s *Store) StageRestore(
	ctx context.Context, a backup.StagingArea, in backup.RestoreStageInput,
) (backup.Summary, backup.DevStripReport, error) {
	var none backup.DevStripReport

	path, err := s.areaPath(a)
	if err != nil {
		return backup.Summary{}, none, err
	}

	index, err := s.portableStage(ctx, in.Want, in.Files, in.Options)
	if err != nil {
		return backup.Summary{}, none, err
	}

	// The areas of the earlier tokens of this restore, and a itself, go
	// first. An area of a higher token belongs to a newer holder.
	earlier := func(b backup.StagingArea) bool { return b.Restore == a.Restore && b.Token <= a.Token }
	if removeErr := s.removeAreas(earlier); removeErr != nil {
		return backup.Summary{}, none, removeErr
	}

	sum, err := s.buildCheckedStaging(ctx, path, in.Open, portableCheck{
		index: index, want: in.Want, restoreAt: in.Options.RestoreAt,
	})
	if err != nil {
		return backup.Summary{}, none, err
	}

	if !in.Strip {
		return sum, none, nil
	}

	report, err := stripStagedFile(ctx, path, in.StripMode)
	if err != nil {
		return backup.Summary{}, none, errors.Join(backup.ErrDevStripIncomplete, err, removeSnapshotErr(path))
	}

	return sum, report, nil
}

// removeAreas removes the file of each staging area of this database that
// drop selects.
func (s *Store) removeAreas(drop func(backup.StagingArea) bool) error {
	areas, err := s.listAreas()
	if err != nil {
		return err
	}

	for _, b := range areas {
		if !drop(b) {
			continue
		}

		path, err := s.areaPath(b)
		if err != nil {
			return err
		}

		if err := removeSnapshotErr(path); err != nil {
			return fmt.Errorf("remove staging area %s: %w", b, err)
		}
	}

	return nil
}

// ApplyRestoreArea implements backup.RestoreStaging. The rename of a file
// cannot share a transaction with SQL, so the swap checks f on the live
// database under the store write lock, and records the step in the staged
// file, in its fence transaction. The rename then makes the record live with
// the restored data, or neither.
func (s *Store) ApplyRestoreArea(
	ctx context.Context, f durable.StepFence, a backup.StagingArea, keepAccess bool,
) (backup.KeepAccessResult, error) {
	path, err := s.areaPath(a)
	if err != nil {
		return backup.KeepAccessResult{}, err
	}

	var (
		row db.DurableOperation
		now int64
	)

	plan := swapPlan{
		staged: path, keepAccess: keepAccess,
		before: func(ctx context.Context) error {
			// The fence comes first, before the first change, so a cancel
			// before the swap and the swap have one winner.
			return s.runTx(ctx, func(q *db.Queries) error {
				var fenceErr error

				row, now, fenceErr = fencedRestore(ctx, q, f, a.Restore)
				if fenceErr != nil {
					return fenceErr
				}

				// The swap runs only in maintenance mode, so no tool traffic
				// writes during it. The store write lock holds the state
				// until the rename.
				state, stateErr := q.GetInstallationState(ctx)
				if stateErr != nil {
					return fmt.Errorf("read installation state sqlite database: %w", maintenance.ErrUnavailable)
				}

				if maintenance.State(state) != maintenance.StateMaintenance {
					return fmt.Errorf("swap restore area %s in state %q: %w", a, state, maintenance.ErrStateChanged)
				}

				if _, statErr := os.Stat(path); statErr != nil {
					return fmt.Errorf("apply staging area %s: %w", a, backup.ErrCorrupt)
				}

				return nil
			}, nil)
		},
		after: func(ctx context.Context, tx *sql.Tx, kept backup.KeepAccessResult) error {
			return insertStep(ctx, db.New(tx), stepAt{row: row, now: now}, f, backup.EncodeRestoreSwap(kept))
		},
	}

	dropped, err := s.swapStaged(ctx, plan)
	if err != nil {
		return backup.KeepAccessResult{}, err
	}

	return backup.KeepAccessResult{DroppedTempAdmins: dropped}, nil
}

// CleanRestoreAreas implements backup.RestoreStaging. SQLite keeps no old
// live data after the swap: the rename replaced the file. The removals of
// the files cannot share a transaction with SQL, so the cleanup checks f
// before the first removal and records the step after the last one.
func (s *Store) CleanRestoreAreas(
	ctx context.Context, f durable.StepFence, id backup.RestoreID, _ time.Duration,
) ([]backup.StagingArea, error) {
	if _, err := backup.ParseRestoreID(id.String()); err != nil {
		return nil, fmt.Errorf("clean restore areas: %w", err)
	}

	err := func() error {
		s.mu.RLock()
		defer s.mu.RUnlock()

		return s.runTx(ctx, func(q *db.Queries) error {
			_, _, fenceErr := fencedRestore(ctx, q, f, id)

			return fenceErr
		}, nil)
	}()
	if err != nil {
		return nil, err
	}

	dropped, err := s.removeEndedAreas(ctx, id)
	if err != nil {
		return dropped, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	recordErr := s.inControlTx(ctx, func(q *db.Queries) error {
		return recordedRestore(ctx, q, f, id)
	})
	if recordErr != nil {
		return dropped, recordErr
	}

	return dropped, nil
}

// removeEndedAreas removes the file of each staging area of restore id and of
// each restore whose row ended. An area of a restore with no row here stays:
// another installation can share the directory. It returns the areas that it
// removed.
func (s *Store) removeEndedAreas(ctx context.Context, id backup.RestoreID) ([]backup.StagingArea, error) {
	rows, err := s.ListRestores(ctx)
	if err != nil {
		return nil, err
	}

	ended := map[backup.RestoreID]bool{id: true}

	for i := range rows {
		if rows[i].State == backup.RestoreSucceeded || rows[i].State == backup.RestoreFailed {
			ended[rows[i].ID] = true
		}
	}

	areas, err := s.listAreas()
	if err != nil {
		return nil, err
	}

	var dropped []backup.StagingArea

	for _, a := range areas {
		if !ended[a.Restore] {
			continue
		}

		path, pathErr := s.areaPath(a)
		if pathErr != nil {
			return dropped, pathErr
		}

		if removeErr := removeSnapshotErr(path); removeErr != nil {
			return dropped, fmt.Errorf("remove staging area %s: %w", a, removeErr)
		}

		dropped = append(dropped, a)
	}

	return dropped, nil
}

// ListRestoreAreas implements backup.RestoreStaging.
func (s *Store) ListRestoreAreas(context.Context) ([]backup.StagingArea, error) {
	return s.listAreas()
}

// listAreas returns the staging areas next to the live database, ordered by
// restore and token.
func (s *Store) listAreas() ([]backup.StagingArea, error) {
	dir := filepath.Dir(s.path)
	prefix := filepath.Base(s.path) + areaInfix

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("list staging areas: %w", err)
	}

	var out []backup.StagingArea

	for _, e := range entries {
		rest, ok := strings.CutPrefix(e.Name(), prefix)
		if !ok || !e.Type().IsRegular() {
			continue
		}

		m := areaSuffixPattern.FindStringSubmatch(rest)
		if m == nil {
			continue
		}

		token, err := strconv.ParseInt(m[2], 10, 64)
		if err != nil {
			continue
		}

		if a, err := backup.NewStagingArea(backup.RestoreID(m[1]), token); err == nil {
			out = append(out, a)
		}
	}

	slices.SortFunc(out, func(x, y backup.StagingArea) int {
		if c := strings.Compare(x.Restore.String(), y.Restore.String()); c != 0 {
			return c
		}

		return int(x.Token - y.Token)
	})

	return out, nil
}

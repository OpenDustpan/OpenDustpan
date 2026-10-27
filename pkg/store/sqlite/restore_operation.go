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
var areaSuffixPattern = regexp.MustCompile(`^(rst-[a-z2-7]{20})-([1-9][0-9]{0,18})$`)

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
// transaction of q. With record it also records the step of f with result.
// It returns the row of the operation and the time of the store clock.
func fencedRestore(
	ctx context.Context, q *db.Queries, f durable.StepFence, id backup.RestoreID, result []byte, record bool,
) (db.DurableOperation, int64, error) {
	row, now, err := checkStep(ctx, q, f)
	if err != nil {
		return db.DurableOperation{}, 0, err
	}

	// Defense in depth: the fence checked above is live, but it must belong
	// to the operation of this restore. The service passes the right fence;
	// this check stays so that no caller can write the row of one restore
	// under the claim of another operation.
	if err := ownsRestore(ctx, q, f, id); err != nil {
		return db.DurableOperation{}, 0, err
	}

	if record {
		if err := insertStep(ctx, q, row, f, result, now); err != nil {
			return db.DurableOperation{}, 0, err
		}
	}

	return row, now, nil
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
	if err := start.Validate(); err != nil {
		return fmt.Errorf("start restore %s: %w", r.ID, err)
	}

	if _, err := backup.ParseRestoreID(r.ID.String()); err != nil {
		return fmt.Errorf("start restore: %w", err)
	}

	// Defense in depth: the service builds the request from the restore ID.
	// This check stays so that no caller can start the row of one restore
	// with the operation of another kind or key.
	if start.Key != backup.RestoreOperationKey(r.ID) || start.Kind != backup.RestoreOperationKind {
		return fmt.Errorf("start restore %s: operation %q of kind %q: %w",
			r.ID, start.Key, start.Kind, durable.ErrKeyConflict)
	}

	report := ""

	if r.DevReport != nil {
		text, err := codec.EncodeDevReport(*r.DevReport)
		if err != nil {
			return err
		}

		report = text
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.inControlTx(ctx, func(q *db.Queries) error {
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
	})
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
		if _, _, err := fencedRestore(ctx, q, f, id, nil, true); err != nil {
			return err
		}

		if _, err := runningRestore(ctx, q, id); err != nil {
			return err
		}

		if _, err := q.SetRestoreDevReport(ctx, db.SetRestoreDevReportParams{DevReport: text, ID: id.String()}); err != nil {
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
		if _, _, err := fencedRestore(ctx, q, f, id, nil, false); err != nil {
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
	ctx context.Context, f durable.StepFence, id backup.RestoreID, want maintenance.State,
) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var entered bool

	err := s.inControlTx(ctx, func(q *db.Queries) error {
		row, now, err := fencedRestore(ctx, q, f, id, nil, false)
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

		return insertStep(ctx, q, row, f, backup.EncodeRestoreMaintenance(entered), now)
	})
	if err != nil {
		return false, err
	}

	return entered, nil
}

// MarkRestoreApplying implements backup.RestoreOperations.
func (s *Store) MarkRestoreApplying(
	ctx context.Context, f durable.StepFence, id backup.RestoreID, preBackupID backup.ID,
) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.inControlTx(ctx, func(q *db.Queries) error {
		if _, _, err := fencedRestore(ctx, q, f, id, nil, true); err != nil {
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

// AppendRestoreAudit implements backup.RestoreOperations.
func (s *Store) AppendRestoreAudit(
	ctx context.Context, f durable.StepFence, id backup.RestoreID, e audit.Entry,
) (audit.Entry, error) {
	if err := validServerEntry(e); err != nil {
		return audit.Entry{}, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	var stored audit.Entry

	err := s.inControlTx(ctx, func(q *db.Queries) error {
		if _, _, err := fencedRestore(ctx, q, f, id, nil, true); err != nil {
			return err
		}

		row, err := runningRestore(ctx, q, id)
		if err != nil {
			return err
		}

		if row.State != string(backup.RestoreApplying) {
			return backup.ErrRestoreNotRunning
		}

		stored, err = appendAuditEntry(ctx, q, e)

		return err
	})
	if err != nil {
		return audit.Entry{}, err
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
		if _, _, err := fencedRestore(ctx, q, f, end.ID, nil, true); err != nil {
			return err
		}

		row, err := runningRestore(ctx, q, end.ID)
		if err != nil {
			return err
		}

		if err := endRestore(ctx, q, end, row.MaintenanceEntered != 0 && end.LeaveMaintenance); err != nil {
			return err
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

// endRestore ends the running restore end.ID in the transaction of q. With
// leave, it moves the installation from maintenance to normal operation
// first, with a compare-and-set: a state that is already normal stays.
func endRestore(ctx context.Context, q *db.Queries, end backup.RestoreEnd, leave bool) error {
	if leave {
		if _, err := q.SetInstallationStateIf(ctx, db.SetInstallationStateIfParams{
			State: string(maintenance.StateNormal), State_2: string(maintenance.StateMaintenance),
		}); err != nil {
			return fmt.Errorf("leave maintenance mode at the end of restore %s: %w", end.ID, maintenance.ErrUnavailable)
		}
	}

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
		op, err := q.GetDurableOperationByKey(ctx, backup.RestoreOperationKey(id))
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}

		if err != nil {
			return fmt.Errorf("select the operation of restore %s: %w", id, err)
		}

		text := ""

		switch durable.State(op.State) {
		case durable.StateCanceled:
			text = backup.RestoreCanceledText
		case durable.StateFailed:
			text = backup.RestoreStoppedText
		case durable.StatePending, durable.StateRunning, durable.StateSucceeded:
			return nil
		default:
			return fmt.Errorf("durable operation of restore %s has the unknown state %q: %w", id, op.State,
				errUnknownDurableState)
		}

		row, err := runningRestore(ctx, q, id)
		if errors.Is(err, backup.ErrRestoreNotRunning) || errors.Is(err, backup.ErrRestoreNotFound) {
			return nil
		}

		if err != nil {
			return err
		}

		end := backup.RestoreEnd{
			ID: id, State: backup.RestoreFailed, Error: text, EndedAt: endedAt, DevReport: nil, LeaveMaintenance: leave,
		}

		if err := endRestore(ctx, q, end, leave && row.MaintenanceEntered != 0); err != nil {
			return err
		}

		got, err := q.GetRestore(ctx, id.String())
		if err != nil {
			return fmt.Errorf("select failed restore: %w", err)
		}

		out, err = restoreFromRow(got)
		ended = err == nil

		return err
	})

	return out, ended, err
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
	if err := s.removeAreas(func(b backup.StagingArea) bool {
		return b.Restore == a.Restore && b.Token <= a.Token
	}); err != nil {
		return backup.Summary{}, none, err
	}

	sum, err := s.buildCheckedStaging(ctx, path, in.Open, index, in.Want)
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

				row, now, fenceErr = fencedRestore(ctx, q, f, a.Restore, nil, false)
				if fenceErr != nil {
					return fenceErr
				}

				if _, statErr := os.Stat(path); statErr != nil {
					return fmt.Errorf("apply staging area %s: %w", a, backup.ErrCorrupt)
				}

				return nil
			}, nil)
		},
		after: func(ctx context.Context, tx *sql.Tx) error {
			return insertStep(ctx, db.New(tx), row, f, nil, now)
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
			_, _, fenceErr := fencedRestore(ctx, q, f, id, nil, false)

			return fenceErr
		}, nil)
	}()
	if err != nil {
		return nil, err
	}

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
		// An area of a restore with no row here stays: another installation
		// can share the directory.
		if !ended[a.Restore] {
			continue
		}

		path, err := s.areaPath(a)
		if err != nil {
			return dropped, err
		}

		if err := removeSnapshotErr(path); err != nil {
			return dropped, fmt.Errorf("remove staging area %s: %w", a, err)
		}

		dropped = append(dropped, a)
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	if err := s.inControlTx(ctx, func(q *db.Queries) error {
		_, _, recordErr := fencedRestore(ctx, q, f, id, nil, true)

		return recordErr
	}); err != nil {
		return dropped, err
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

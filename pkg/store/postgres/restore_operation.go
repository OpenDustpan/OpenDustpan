package postgres

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
)

// The schema names of the areas of a restore that runs as a durable
// operation. A staging area is restore_stage_<id>_<token>, and the old live
// schema after the swap is restore_prev_<id>, where <id> is the restore ID
// without its prefix. Each name holds letters, digits and underscores only.
const (
	areaPrefix     = stagingSchemaPrefix
	previousPrefix = "restore_prev_"
	restoreIDHead  = "rst-"
)

// areaPattern matches the name of a staging area of a restore, and no other
// staging schema: an older staging schema has 16 hex digits after the
// prefix.
var areaPattern = regexp.MustCompile(`^` + areaPrefix + `([a-z2-7]{20})_([1-9]\d{0,18})$`)

// areaSchema returns the schema of the staging area a. It checks a again,
// so a value that the service did not build never reaches a schema name.
func areaSchema(a backup.StagingArea) (string, error) {
	// Defense in depth: the service builds each area with NewStagingArea;
	// this check stays so that no caller of the port can put another value
	// into a schema name. The quoting of pgIdent does not rely on it.
	checked, err := backup.NewStagingArea(a.Restore, a.Token)
	if err != nil {
		return "", err
	}

	return areaPrefix + strings.TrimPrefix(checked.Restore.String(), restoreIDHead) + "_" +
		strconv.FormatInt(checked.Token, 10), nil
}

// previousSchemaOf returns the schema that holds the old live data of
// restore id after its swap.
func previousSchemaOf(id backup.RestoreID) (string, error) {
	checked, err := backup.ParseRestoreID(id.String())
	if err != nil {
		return "", err
	}

	return previousPrefix + strings.TrimPrefix(checked.String(), restoreIDHead), nil
}

// parseAreaSchema returns the area of the schema name, and false for any
// name that areaSchema does not make.
func parseAreaSchema(name string) (backup.StagingArea, bool) {
	m := areaPattern.FindStringSubmatch(name)
	if m == nil {
		return backup.StagingArea{Restore: "", Token: 0}, false
	}

	token, err := strconv.ParseInt(m[2], 10, 64)
	if err != nil {
		return backup.StagingArea{Restore: "", Token: 0}, false
	}

	area, err := backup.NewStagingArea(backup.RestoreID(restoreIDHead+m[1]), token)

	return area, err == nil
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
		return row, now, err
	}

	// Defense in depth: the fence checked above is live, but it must belong
	// to the operation of this restore. The service passes the right fence;
	// this check stays so that no caller can write the row of one restore
	// under the claim of another operation.
	if ownErr := ownsRestore(ctx, q, f, id); ownErr != nil {
		return row, now, ownErr
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

// runningRestore returns the state and the maintenance flag of the running
// restore id in the transaction of q: ErrRestoreNotFound when it does not
// exist, and ErrRestoreNotRunning when it ended.
func runningRestore(ctx context.Context, q *db.Queries, id backup.RestoreID) (db.GetRestoreMaintenanceRow, error) {
	row, err := q.GetRestoreMaintenance(ctx, id.String())
	if errors.Is(err, pgx.ErrNoRows) {
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

	_, created, err := startOperation(ctx, q, start)
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
	return s.setStateIfWithin(ctx, expected, next, func(q *db.Queries) error {
		if _, _, err := fencedRestore(ctx, q, f, id); err != nil {
			return err
		}

		if _, err := runningRestore(ctx, q, id); err != nil {
			return err
		}

		_, err := q.SetRestoreMaintenanceEntered(ctx, db.SetRestoreMaintenanceEnteredParams{
			MaintenanceEntered: next == maintenance.StateMaintenance,
			ID:                 id.String(),
		})
		if err != nil {
			return fmt.Errorf("store the maintenance flag of restore %s: %w", id, err)
		}

		return nil
	})
}

// ConfirmRestoreMaintenance implements backup.RestoreOperations.
func (s *Store) ConfirmRestoreMaintenance(
	ctx context.Context, f durable.StepFence, id backup.RestoreID, want maintenance.State, canceled []string,
) (bool, error) {
	var entered bool

	err := s.inControlTx(ctx, func(q *db.Queries) error {
		row, now, err := checkStep(ctx, q, f)
		if err != nil {
			return err
		}

		if ownErr := ownsRestore(ctx, q, f, id); ownErr != nil {
			return ownErr
		}

		restore, err := runningRestore(ctx, q, id)
		if err != nil {
			return err
		}

		state, err := q.GetInstallationState(ctx)
		if err != nil {
			return fmt.Errorf("read installation state postgres database: %w", maintenance.ErrUnavailable)
		}

		if maintenance.State(state) != want {
			return fmt.Errorf("installation state is not %q: %w", want, maintenance.ErrStateChanged)
		}

		entered = restore.MaintenanceEntered

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
	ctx context.Context, f durable.StepFence, id backup.RestoreID, preBackupID backup.ID,
) error {
	return s.inControlTx(ctx, func(q *db.Queries) error {
		if err := recordedRestore(ctx, q, f, id); err != nil {
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

	return s.inControlTx(ctx, func(q *db.Queries) error {
		if _, _, err := fencedRestore(ctx, q, f, id); err != nil {
			return err
		}

		row, err := q.GetRestoreAuditEvents(ctx, id.String())
		if errors.Is(err, pgx.ErrNoRows) {
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

// RestoreEvents implements backup.RestoreOperations.
func (s *Store) RestoreEvents(ctx context.Context, id backup.RestoreID) ([]string, error) {
	row, err := s.queries.GetRestoreAuditEvents(ctx, id.String())
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, backup.ErrRestoreNotFound
	}

	if err != nil {
		return nil, fmt.Errorf("select restore audit events: %w", err)
	}

	return backup.DecodeRestoreEvents(row.AuditEvents)
}

// RestoreMaintenanceEntered implements backup.RestoreOperations.
func (s *Store) RestoreMaintenanceEntered(ctx context.Context, id backup.RestoreID) (bool, error) {
	row, err := s.queries.GetRestoreMaintenance(ctx, id.String())
	if errors.Is(err, pgx.ErrNoRows) {
		return false, backup.ErrRestoreNotFound
	}

	if err != nil {
		return false, fmt.Errorf("select restore: %w", err)
	}

	return row.MaintenanceEntered, nil
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
	// The scope lock comes first, as in every append of the scope, so the
	// appends form one sequence and no lock order can deadlock.
	if err := q.LockServerAuditScope(ctx); err != nil {
		return nil, fmt.Errorf("lock server audit scope: %w", err)
	}

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

		out, err = restoreRecord(got)

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
	if row.MaintenanceEntered && end.LeaveMaintenance {
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
		EndedAtMs: backupEndedAt(end.EndedAt),
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
func endedRestoreText(state string) (string, bool, error) {
	switch durable.State(state) {
	case durable.StateCanceled:
		return backup.RestoreCanceledText, true, nil
	case durable.StateFailed:
		return backup.RestoreStoppedText, true, nil
	case durable.StatePending, durable.StateRunning, durable.StateSucceeded:
		return "", false, nil
	default:
		return "", false, fmt.Errorf("%w: state %q", errUnknownDurableState, state)
	}
}

// failEndedRestore marks the running restore id failed when its operation
// ended canceled or failed, in one transaction. It reports whether it did.
func (s *Store) failEndedRestore(
	ctx context.Context, id backup.RestoreID, endedAt time.Time, leave bool,
) (backup.RestoreRecord, bool, error) {
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
	if errors.Is(err, pgx.ErrNoRows) {
		return none, false, nil
	}

	if err != nil {
		return none, false, fmt.Errorf("select the operation of restore %s: %w", id, err)
	}

	text, ended, err := endedRestoreText(op.State)
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

	out, err := restoreRecord(got)
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

	schema, err := areaSchema(a)
	if err != nil {
		return backup.Summary{}, none, err
	}

	stage, err := s.portableStage(ctx, in.Open, in.Want, in.Files, in.Options)
	if err != nil {
		return backup.Summary{}, none, err
	}

	// The areas of the earlier tokens of this restore, and a itself, go
	// first. An area of a higher token belongs to a newer holder.
	earlier := func(b backup.StagingArea) bool { return b.Restore == a.Restore && b.Token <= a.Token }
	if dropErr := s.dropAreas(ctx, earlier); dropErr != nil {
		return backup.Summary{}, none, dropErr
	}

	stage.staging = schema

	sum, err := s.buildChecked(ctx, stage)
	if err != nil {
		return backup.Summary{}, none, err
	}

	if in.DropIssuerKeys {
		if dropErr := s.dropStagedIssuerKeys(ctx, schema); dropErr != nil {
			return backup.Summary{}, none, errors.Join(dropErr, s.dropStagingSchema(context.WithoutCancel(ctx), schema))
		}
	}

	if !in.Strip {
		return sum, none, nil
	}

	report, err := s.stripSchema(ctx, schema, in.StripMode)
	if err != nil {
		return backup.Summary{}, none, errors.Join(err, s.dropStagingSchema(context.WithoutCancel(ctx), schema))
	}

	return sum, report, nil
}

// dropAreas drops each staging area of this database that keep selects.
func (s *Store) dropAreas(ctx context.Context, drop func(backup.StagingArea) bool) error {
	areas, err := s.ListRestoreAreas(ctx)
	if err != nil {
		return err
	}

	for _, b := range areas {
		if !drop(b) {
			continue
		}

		schema, schemaErr := areaSchema(b)
		if schemaErr != nil {
			return schemaErr
		}

		if dropErr := s.dropStagingSchema(ctx, schema); dropErr != nil {
			return dropErr
		}
	}

	return nil
}

// ApplyRestoreArea implements backup.RestoreStaging.
func (s *Store) ApplyRestoreArea(
	ctx context.Context, f durable.StepFence, a backup.StagingArea, keepAccess bool,
) (backup.KeepAccessResult, error) {
	schema, err := areaSchema(a)
	if err != nil {
		return backup.KeepAccessResult{}, err
	}

	previous, err := previousSchemaOf(a.Restore)
	if err != nil {
		return backup.KeepAccessResult{}, err
	}

	exists, err := s.schemaExists(ctx, schema)
	if err != nil {
		return backup.KeepAccessResult{}, err
	}

	var (
		row db.DurableOperation
		now int64
	)

	plan := swapPlan{
		staging: schema, previous: previous, keepAccess: keepAccess,
		before: func(tx pgx.Tx) error {
			// The fence comes first in the swap transaction and locks the row
			// of the operation, so a cancel before the swap and the swap have
			// one winner. The record of the step follows at the end of the
			// same transaction, with the result of the swap.
			q := s.queries.WithTx(tx)

			var fenceErr error

			if row, now, fenceErr = fencedRestore(ctx, q, f, a.Restore); fenceErr != nil {
				return fenceErr
			}

			// The swap runs only in maintenance mode, so no tool traffic
			// writes during it. The lock holds the state until the commit.
			state, stateErr := q.LockInstallationState(ctx)
			if stateErr != nil {
				return fmt.Errorf("read installation state postgres database: %w", maintenance.ErrUnavailable)
			}

			if maintenance.State(state) != maintenance.StateMaintenance {
				return fmt.Errorf("swap restore area %s in state %q: %w", a, state, maintenance.ErrStateChanged)
			}

			if !exists {
				return fmt.Errorf("apply staging area %s: %w", a, backup.ErrCorrupt)
			}

			return nil
		},
		after: func(tx pgx.Tx, kept backup.KeepAccessResult) error {
			return insertStep(ctx, s.queries.WithTx(tx), stepAt{row: row, now: now}, f, backup.EncodeRestoreSwap(kept))
		},
	}

	dropped, err := s.swapStaging(ctx, plan)
	if err != nil {
		return backup.KeepAccessResult{}, err
	}

	return backup.KeepAccessResult{DroppedTempAdmins: dropped}, nil
}

// schemaExists reports whether the schema name exists in this database.
func (s *Store) schemaExists(ctx context.Context, name string) (bool, error) {
	var n int

	if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM pg_namespace WHERE nspname = $1", name).Scan(&n); err != nil {
		return false, fmt.Errorf("read schema %q: %w", name, err)
	}

	return n > 0, nil
}

// CleanRestoreAreas implements backup.RestoreStaging.
func (s *Store) CleanRestoreAreas(
	ctx context.Context, f durable.StepFence, id backup.RestoreID, lockWait time.Duration,
) ([]backup.StagingArea, error) {
	if _, err := backup.ParseRestoreID(id.String()); err != nil {
		return nil, fmt.Errorf("clean restore areas: %w", err)
	}

	areas, err := s.ListRestoreAreas(ctx)
	if err != nil {
		return nil, err
	}

	rows, err := s.ListRestores(ctx)
	if err != nil {
		return nil, err
	}

	previousNames, err := s.schemasWithPrefix(ctx, previousPrefix)
	if err != nil {
		return nil, err
	}

	drop, previous := cleanPlan(id, areas, rows, previousNames)

	names, err := cleanSchemas(drop, previous)
	if err != nil {
		return nil, err
	}

	err = inBackupTx(ctx, s.pool, readWriteOpts(), func(tx pgx.Tx) error {
		// The fence comes before the first drop, and the record of the step
		// commits with the drops.
		if fenceErr := recordedRestore(ctx, s.queries.WithTx(tx), f, id); fenceErr != nil {
			return fenceErr
		}

		return dropCleanSchemas(ctx, tx, names, lockWait)
	})
	if err != nil {
		return nil, err
	}

	return drop, nil
}

// cleanSchemas returns the schemas of the areas drop, with their control
// schemas, and the previous schemas.
func cleanSchemas(drop []backup.StagingArea, previous []string) ([]string, error) {
	names := make([]string, 0, 2*len(drop)+len(previous))

	for _, a := range drop {
		schema, err := areaSchema(a)
		if err != nil {
			return nil, err
		}

		names = append(names, schema, schema+"_control")
	}

	return append(names, previous...), nil
}

// dropCleanSchemas drops the schemas names in tx. Each drop waits for its
// locks for lockWait at most, and a drop that waits longer is ErrRestoreBusy.
func dropCleanSchemas(ctx context.Context, tx pgx.Tx, names []string, lockWait time.Duration) error {
	wait := strconv.FormatInt(max(lockWait, time.Millisecond).Milliseconds(), 10) + "ms"
	if _, err := tx.Exec(ctx, "SELECT set_config('lock_timeout', $1, true)", wait); err != nil {
		return fmt.Errorf("set the lock timeout of the cleanup: %w", err)
	}

	for _, name := range names {
		// Defense in depth: every name comes from areaSchema or
		// previousSchemaOf; this check stays so that a defect there never
		// drops a live schema.
		if !strings.HasPrefix(name, areaPrefix) && !strings.HasPrefix(name, previousPrefix) {
			return fmt.Errorf("drop schema %q: %w", name, backup.ErrCorrupt)
		}

		if _, err := tx.Exec(ctx, "DROP SCHEMA IF EXISTS "+pgIdent(name)+" CASCADE"); err != nil {
			if isLockTimeout(err) {
				return fmt.Errorf("drop schema %q: %w", name, backup.ErrRestoreBusy)
			}

			return fmt.Errorf("drop schema %q: %w", name, err)
		}
	}

	return nil
}

// cleanPlan returns the staging areas and the previous schemas that the
// cleanup of restore id drops: each area and the previous schema of id, and
// those of each restore whose row in rows ended. An area or a previous schema
// of a restore with no row stays: another installation can share the
// database. existing names the previous schemas of the database.
func cleanPlan(
	id backup.RestoreID, areas []backup.StagingArea, rows []backup.RestoreRecord, existing []string,
) ([]backup.StagingArea, []string) {
	ended := map[backup.RestoreID]bool{id: true}

	for i := range rows {
		if rows[i].State == backup.RestoreSucceeded || rows[i].State == backup.RestoreFailed {
			ended[rows[i].ID] = true
		}
	}

	drop := make([]backup.StagingArea, 0, len(areas))

	for _, a := range areas {
		if ended[a.Restore] {
			drop = append(drop, a)
		}
	}

	previous := make([]string, 0, len(existing))

	for restore := range ended {
		if name, err := previousSchemaOf(restore); err == nil && slices.Contains(existing, name) {
			previous = append(previous, name)
		}
	}

	slices.Sort(previous)

	return drop, previous
}

// schemasWithPrefix returns the names of the schemas of this database that
// start with prefix, in order.
func (s *Store) schemasWithPrefix(ctx context.Context, prefix string) ([]string, error) {
	rows, err := s.pool.Query(ctx,
		"SELECT nspname FROM pg_namespace WHERE starts_with(nspname, $1) ORDER BY nspname", prefix)
	if err != nil {
		return nil, fmt.Errorf("list schemas: %w", err)
	}

	names, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("list schemas: %w", err)
	}

	return names, nil
}

// ListRestoreAreas implements backup.RestoreStaging.
func (s *Store) ListRestoreAreas(ctx context.Context) ([]backup.StagingArea, error) {
	names, err := s.schemasWithPrefix(ctx, areaPrefix)
	if err != nil {
		return nil, err
	}

	var out []backup.StagingArea

	for _, name := range names {
		if a, ok := parseAreaSchema(name); ok {
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

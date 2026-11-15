package backup_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/maintenance"
)

// The fake restore operation ports of restoreServiceLive: the restores rows
// of its registry, the durable operations of its store, and the staging
// areas, with the fence rules of the SQL adapters.

// checkFence checks f against the store of durable operations of l, and
// that its operation runs restore id, with the fence rules of the adapters.
func (l *restoreServiceLive) checkFence(ctx context.Context, f durable.StepFence, id backup.RestoreID) error {
	op, err := l.ops.Get(ctx, f.Operation)
	if err != nil || op.Key != backup.RestoreOperationKey(id) {
		return durable.ErrClaimLost
	}

	_, done := op.Step(f.Step)

	switch {
	case op.State != durable.StateRunning || op.Claim.Token != f.Token:
		return durable.ErrClaimLost
	case op.CancelRequested:
		return durable.ErrCanceled
	case done:
		return durable.ErrStepDone
	default:
		return nil
	}
}

// recordFence records the step of f with result, as the domain write of an
// adapter does, after the check that its operation runs restore id.
func (l *restoreServiceLive) recordFence(
	ctx context.Context, f durable.StepFence, id backup.RestoreID, result []byte,
) error {
	op, err := l.ops.Get(ctx, f.Operation)
	if err != nil || op.Key != backup.RestoreOperationKey(id) {
		return durable.ErrClaimLost
	}

	if err := l.ops.RecordStep(ctx, f, result); err != nil {
		return fmt.Errorf("record step %q: %w", f.Step, err)
	}

	return nil
}

// requireRunning returns ErrRestoreNotRunning unless restore id runs.
func (l *restoreServiceLive) requireRunning(ctx context.Context, id backup.RestoreID) error {
	row, err := l.registry.GetRestore(ctx, id)
	if err != nil {
		return err
	}

	if row.State != backup.RestoreChecking && row.State != backup.RestoreApplying {
		return backup.ErrRestoreNotRunning
	}

	return nil
}

// StartRestore implements backup.RestoreOperations.
func (l *restoreServiceLive) StartRestore(
	ctx context.Context,
	r backup.RestoreRecord,
	start durable.StartRequest,
) error {
	if err := l.registry.CreateRestore(ctx, r); err != nil {
		return err
	}

	if _, _, err := l.ops.Start(ctx, start); err != nil {
		return fmt.Errorf("start the operation: %w", err)
	}

	return nil
}

// RecordRestoreReport implements backup.RestoreOperations.
func (l *restoreServiceLive) RecordRestoreReport(
	ctx context.Context, f durable.StepFence, id backup.RestoreID, report backup.DevStripReport,
) error {
	if err := l.recordFence(ctx, f, id, nil); err != nil {
		return err
	}

	l.mu.Lock()
	l.dev.records = append(l.dev.records, report)

	if l.dev.order != nil {
		l.dev.order.add("record")
	}
	l.mu.Unlock()

	return l.registry.setDevReport(ctx, id, report)
}

// SetRestoreMaintenance implements backup.RestoreOperations.
func (l *restoreServiceLive) SetRestoreMaintenance(
	ctx context.Context, f durable.StepFence, id backup.RestoreID, expected, next maintenance.State,
) error {
	if err := l.checkFence(ctx, f, id); err != nil {
		return err
	}

	if err := l.requireRunning(ctx, id); err != nil {
		return err
	}

	if err := l.maint.compareAndSet(expected, next); err != nil {
		return err
	}

	l.mu.Lock()
	l.entered[id] = next == maintenance.StateMaintenance
	l.mu.Unlock()

	return nil
}

// ConfirmRestoreMaintenance implements backup.RestoreOperations.
func (l *restoreServiceLive) ConfirmRestoreMaintenance(
	ctx context.Context, f durable.StepFence, id backup.RestoreID, want maintenance.State, canceled []string,
) (bool, error) {
	if err := l.checkFence(ctx, f, id); err != nil {
		return false, err
	}

	if err := l.requireRunning(ctx, id); err != nil {
		return false, err
	}

	if got := l.maint.current(); got != want {
		return false, fmt.Errorf("installation state is not %q: %w", want, maintenance.ErrStateChanged)
	}

	l.mu.Lock()
	entered := l.entered[id]
	l.mu.Unlock()

	result, err := backup.EncodeRestoreMaintenance(backup.RestoreMaintenanceResult{
		Entered: entered, Canceled: canceled, Token: f.Token,
	})
	if err != nil {
		return false, err
	}

	return entered, l.recordFence(ctx, f, id, result)
}

// CheckRestoreFence implements backup.RestoreOperations.
func (l *restoreServiceLive) CheckRestoreFence(ctx context.Context, f durable.StepFence, id backup.RestoreID) error {
	if err := l.checkFence(ctx, f, id); err != nil {
		return err
	}

	return l.requireRunning(ctx, id)
}

// RecordRestoreEvent implements backup.RestoreOperations.
func (l *restoreServiceLive) RecordRestoreEvent(
	ctx context.Context, f durable.StepFence, id backup.RestoreID, object string,
) error {
	if err := l.checkFence(ctx, f, id); err != nil {
		return err
	}

	if err := l.requireRunning(ctx, id); err != nil {
		return err
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	stored := l.events[id]
	if stored == "" {
		stored = "[]"
	}

	events, _, err := backup.AddRestoreEvent(stored, object)
	if err != nil {
		return err
	}

	l.events[id] = events

	return nil
}

// RestoreEvents implements backup.RestoreOperations.
func (l *restoreServiceLive) RestoreEvents(ctx context.Context, id backup.RestoreID) ([]string, error) {
	if _, err := l.registry.GetRestore(ctx, id); err != nil {
		return nil, err
	}

	l.mu.Lock()
	stored := l.events[id]
	l.mu.Unlock()

	if stored == "" {
		stored = "[]"
	}

	return backup.DecodeRestoreEvents(stored)
}

// RestoreMaintenanceEntered implements backup.RestoreOperations.
func (l *restoreServiceLive) RestoreMaintenanceEntered(ctx context.Context, id backup.RestoreID) (bool, error) {
	if _, err := l.registry.GetRestore(ctx, id); err != nil {
		return false, err
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	return l.entered[id], nil
}

// MarkRestoreApplying implements backup.RestoreOperations.
func (l *restoreServiceLive) MarkRestoreApplying(
	ctx context.Context, f durable.StepFence, id backup.RestoreID, pre backup.ID, result []byte,
) error {
	l.mu.Lock()
	hook := l.onApplying
	l.mu.Unlock()

	if hook != nil {
		hook()
	}

	if err := l.recordFence(ctx, f, id, result); err != nil {
		return err
	}

	return l.registry.MarkRestoreApplying(ctx, id, pre)
}

// AppendRestoreAudit implements backup.RestoreOperations.
func (l *restoreServiceLive) AppendRestoreAudit(
	ctx context.Context, f durable.StepFence, id backup.RestoreID, entries []audit.Entry,
) ([]audit.Entry, error) {
	l.mu.Lock()
	appendErr := l.dev.appendErr
	l.mu.Unlock()

	if appendErr != nil {
		return nil, appendErr
	}

	if err := l.recordFence(ctx, f, id, nil); err != nil {
		return nil, err
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	l.dev.appends = append(l.dev.appends, entries...)

	return slices.Clone(entries), nil
}

// FinishRestore implements backup.RestoreOperations.
func (l *restoreServiceLive) FinishRestore(
	ctx context.Context, f durable.StepFence, end backup.RestoreEnd,
) (backup.RestoreRecord, error) {
	if err := l.recordFence(ctx, f, end.ID, nil); err != nil {
		return backup.RestoreRecord{}, err
	}

	return l.end(ctx, end)
}

// end ends the row of end.ID, and leaves the maintenance mode that the
// restore entered when end says so.
func (l *restoreServiceLive) end(ctx context.Context, end backup.RestoreEnd) (backup.RestoreRecord, error) {
	l.mu.Lock()
	entered := l.entered[end.ID]
	l.entered[end.ID] = false
	l.mu.Unlock()

	if entered && end.LeaveMaintenance {
		l.maint.leaveAtEnd()
	}

	if end.DevReport != nil {
		if err := l.registry.setDevReport(ctx, end.ID, *end.DevReport); err != nil {
			return backup.RestoreRecord{}, err
		}
	}

	return l.registry.FinishRestore(ctx, end.ID, end.State, end.Error, end.EndedAt)
}

// FailEndedRestores implements backup.RestoreOperations.
func (l *restoreServiceLive) FailEndedRestores(
	ctx context.Context, endedAt time.Time, leave bool,
) ([]backup.RestoreRecord, error) {
	rows, err := l.registry.ListRestores(ctx)
	if err != nil {
		return nil, err
	}

	ops, err := l.ops.List(ctx, durable.Filter{Kind: backup.RestoreOperationKind, States: nil, Limit: 0})
	if err != nil {
		return nil, fmt.Errorf("list operations: %w", err)
	}

	texts := make(map[string]string, len(ops))

	for i := range ops {
		if text, ended := endedText(ops[i].State); ended {
			texts[ops[i].Key] = text
		}
	}

	var failed []backup.RestoreRecord

	for i := range rows {
		text, ended := texts[backup.RestoreOperationKey(rows[i].ID)]
		if !ended || (rows[i].State != backup.RestoreChecking && rows[i].State != backup.RestoreApplying) {
			continue
		}

		row, err := l.end(ctx, backup.RestoreEnd{
			ID: rows[i].ID, State: backup.RestoreFailed, Error: text, EndedAt: endedAt, DevReport: nil,
			LeaveMaintenance: leave,
		})
		if err != nil {
			return failed, err
		}

		failed = append(failed, row)
	}

	return failed, nil
}

// endedText returns the error text of a restore whose operation is in
// state, and whether the operation ended with no finish write.
func endedText(state durable.State) (string, bool) {
	text, ended := map[durable.State]string{
		durable.StateCanceled: backup.RestoreCanceledText,
		durable.StateFailed:   backup.RestoreStoppedText,
	}[state]

	return text, ended
}

// StageRestore implements backup.RestoreStaging over the check store of the
// fake, with its strip.
func (l *restoreServiceLive) StageRestore(
	ctx context.Context, a backup.StagingArea, in backup.RestoreStageInput,
) (backup.Summary, backup.DevStripReport, error) {
	var none backup.DevStripReport

	if _, err := backup.NewStagingArea(a.Restore, a.Token); err != nil {
		return backup.Summary{}, none, err
	}

	l.mu.Lock()
	l.staged = append(l.staged, in)
	l.mu.Unlock()

	sum, err := l.StagePortable(ctx, in.Open, in.Want, in.Files, in.Options)
	if err != nil {
		return backup.Summary{}, none, err
	}

	report := none

	if in.Strip {
		report, err = l.StripDevStaged(ctx, in.StripMode)
		if err != nil {
			return backup.Summary{}, none, errors.Join(backup.ErrDevStripIncomplete, err, l.DiscardStaging(ctx))
		}
	}

	l.mu.Lock()
	l.areas = append(l.areas, a)
	after := l.afterStage
	sum.Blobs = append(slices.Clone(sum.Blobs), l.stageBlobs...)
	l.mu.Unlock()

	if after != nil {
		after()
	}

	return sum, report, nil
}

// DropStagedIssuerKeys implements backup.RestoreStaging. It records the
// installation that it got, and answers the report of the test.
func (l *restoreServiceLive) DropStagedIssuerKeys(
	_ context.Context, a backup.StagingArea, keep string,
) ([]backup.IssuerKeysDropped, error) {
	if _, err := backup.NewStagingArea(a.Restore, a.Token); err != nil {
		return nil, err
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	l.keeps = append(l.keeps, keep)

	return append([]backup.IssuerKeysDropped(nil), l.dropReport...), nil
}

// applyMode says how the fake swaps a staging area.
type applyMode struct {
	keepAccess bool
}

// apply swaps the staging area of l into its live data, with the mode m.
func (m applyMode) apply(ctx context.Context, l *restoreServiceLive) (backup.KeepAccessResult, error) {
	if m.keepAccess {
		return l.ApplyStagingKeepingAccess(ctx)
	}

	return backup.KeepAccessResult{DroppedTempAdmins: 0}, l.ApplyStaging(ctx)
}

// ApplyRestoreArea implements backup.RestoreStaging over the applies of the
// fake.
func (l *restoreServiceLive) ApplyRestoreArea(
	ctx context.Context, f durable.StepFence, a backup.StagingArea, keepAccess bool,
) (backup.KeepAccessResult, error) {
	if err := l.checkFence(ctx, f, a.Restore); err != nil {
		return backup.KeepAccessResult{}, err
	}

	if state := l.maint.current(); state != maintenance.StateMaintenance {
		return backup.KeepAccessResult{}, fmt.Errorf("swap in state %q: %w", state, maintenance.ErrStateChanged)
	}

	kept, err := applyMode{keepAccess: keepAccess}.apply(ctx, l)
	if err != nil {
		return backup.KeepAccessResult{}, err
	}

	return kept, l.recordFence(ctx, f, a.Restore, backup.EncodeRestoreSwap(kept))
}

// CleanRestoreAreas implements backup.RestoreStaging.
func (l *restoreServiceLive) CleanRestoreAreas(
	ctx context.Context, f durable.StepFence, id backup.RestoreID, _ time.Duration,
) ([]backup.StagingArea, error) {
	if err := l.recordFence(ctx, f, id, nil); err != nil {
		return nil, err
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	dropped := slices.Clone(l.areas)
	l.areas = nil

	return dropped, nil
}

// ListRestoreAreas implements backup.RestoreStaging.
func (l *restoreServiceLive) ListRestoreAreas(context.Context) ([]backup.StagingArea, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	return slices.Clone(l.areas), nil
}

// current returns the installation state of the fake maintenance.
func (m *restoreServiceMaintenance) current() maintenance.State {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.state
}

// compareAndSet stores next when the state is expected.
func (m *restoreServiceMaintenance) compareAndSet(expected, next maintenance.State) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.state != expected {
		return fmt.Errorf("installation state is not %q: %w", expected, maintenance.ErrStateChanged)
	}

	m.state = next

	return nil
}

// leaveAtEnd moves the state to normal at the end of a failed restore.
func (m *restoreServiceMaintenance) leaveAtEnd() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.leaves++
	m.state = maintenance.StateNormal
}

// Get implements backup.RestoreMaintenance.
func (m *restoreServiceMaintenance) Get(context.Context) (maintenance.State, error) {
	return m.current(), nil
}

// EnterWith implements backup.RestoreMaintenance: it counts the enter, runs
// the cancel sequence of cancelScript when the test set one, and writes
// maintenance through the writer.
func (m *restoreServiceMaintenance) EnterWith(
	ctx context.Context, opts maintenance.EnterOptions,
) (maintenance.EnterResult, error) {
	m.mu.Lock()
	m.enters++

	if m.order != nil {
		m.order.add("enter")
	}

	enterErr, state, script := m.enterErr, m.state, m.cancelScript
	m.mu.Unlock()

	if enterErr != nil {
		return maintenance.EnterResult{Canceled: nil}, enterErr
	}

	if state == maintenance.StateMaintenance {
		return maintenance.EnterResult{Canceled: nil}, nil
	}

	var canceled []string

	if script != nil {
		var err error

		canceled, err = script.run(ctx, opts.Writer)
		if err != nil {
			return maintenance.EnterResult{Canceled: nil}, err
		}
	}

	if err := opts.Writer.SetStateIf(ctx, maintenance.StateNormal, maintenance.StateMaintenance); err != nil {
		return maintenance.EnterResult{Canceled: nil}, fmt.Errorf("enter: %w", err)
	}

	return maintenance.EnterResult{Canceled: canceled}, nil
}

// RecheckWith implements backup.RestoreMaintenance: no work is active.
func (m *restoreServiceMaintenance) RecheckWith(
	context.Context, maintenance.EnterOptions,
) (maintenance.EnterResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return maintenance.EnterResult{Canceled: nil}, m.enterErr
}

// LeaveWith implements backup.RestoreMaintenance.
func (m *restoreServiceMaintenance) LeaveWith(ctx context.Context, w maintenance.StateWriter) error {
	m.mu.Lock()
	m.leaves++
	held, state := m.held, m.state
	m.mu.Unlock()

	if held {
		return maintenance.ErrHeld
	}

	if state == maintenance.StateNormal {
		return nil
	}

	if err := w.SetStateIf(ctx, maintenance.StateMaintenance, maintenance.StateNormal); err != nil {
		return fmt.Errorf("leave: %w", err)
	}

	return nil
}

// Held implements backup.RestoreMaintenance.
func (m *restoreServiceMaintenance) Held(context.Context) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.held, nil
}

// GetRestore implements backup.RestoreOperations.
func (l *restoreServiceLive) GetRestore(ctx context.Context, id backup.RestoreID) (backup.RestoreRecord, error) {
	return l.registry.GetRestore(ctx, id)
}

// ListRestores implements backup.RestoreOperations.
func (l *restoreServiceLive) ListRestores(ctx context.Context) ([]backup.RestoreRecord, error) {
	return l.registry.ListRestores(ctx)
}

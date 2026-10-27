package backup

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"slices"
	"strconv"
	"strings"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/installation"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/secret"
)

// tempKeyPrefix is the first segment of the temporary blob keys of a
// restore: restore-tmp/<restore id>/<claim token>/<blob key>. No sweep, no
// backup and no unit directory lists it.
const tempKeyPrefix = "restore-tmp"

// restoreInput is the input of a restore operation: the request, with the
// pre-backup ID that Start minted.
type restoreInput struct {
	ID               RestoreID `json:"id"`
	BackupID         ID        `json:"backupID"`
	PreBackupID      ID        `json:"preBackupID"`
	CheckOnly        bool      `json:"checkOnly"`
	Force            bool      `json:"force"`
	RequestedBy      string    `json:"requestedBy"`
	KeepAccessTables bool      `json:"keepAccessTables"`
}

// readRestoreInput decodes and checks the input of a restore operation. An
// input that fails is permanent: no retry changes it.
func readRestoreInput(op durable.Operation) (restoreInput, error) {
	var in restoreInput

	decoder := json.NewDecoder(bytes.NewReader(op.Input))
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(&in); err != nil {
		return restoreInput{}, durable.Permanent(fmt.Errorf("decode restore input: %w", ErrCorrupt))
	}

	// Defense in depth: Start checks each ID before it stores the input;
	// these checks stay so that an input from another writer or another
	// version never reaches a key, a schema or a path.
	if _, err := ParseRestoreID(in.ID.String()); err != nil {
		return restoreInput{}, durable.Permanent(err)
	}

	if op.Key != RestoreOperationKey(in.ID) {
		return restoreInput{}, durable.Permanent(fmt.Errorf("restore input of %s under key %q: %w", in.ID, op.Key,
			ErrCorrupt))
	}

	for _, id := range []ID{in.BackupID, in.PreBackupID} {
		if _, err := ParseID(id.String()); err != nil {
			return restoreInput{}, durable.Permanent(err)
		}
	}

	return in, nil
}

// stageResult is the result of the stage step: the staging area, the pinned
// manifest, the live installation kind and the dev data report with its
// warnings.
type stageResult struct {
	Area     string            `json:"area"`
	SQLSize  int64             `json:"sqlSize"`
	SQLSHA   string            `json:"sqlSha256"`
	Schema   int               `json:"schemaVersion"`
	Dialect  Dialect           `json:"dialect"`
	Kind     installation.Kind `json:"kind"`
	Wrappers []string          `json:"wrapperIDs"`
	Live     installation.Kind `json:"liveKind"`
	Report   DevStripReport    `json:"report"`
}

// pinned returns the pinned manifest of r. A checksum that does not decode
// pins nothing that a manifest can match, so the repin fails closed.
func (r stageResult) pinned() pinnedManifest {
	sum, err := hex.DecodeString(r.SQLSHA)
	if err != nil {
		sum = nil
	}

	return pinnedManifest{
		sqlSize: r.SQLSize, sqlSHA: string(sum), schema: r.Schema, dialect: r.Dialect, kind: r.Kind,
		wrappers: r.Wrappers,
	}
}

// stageResultOf returns the stage result of op. A result that does not
// decode is permanent.
func stageResultOf(op durable.Operation) (stageResult, StagingArea, error) {
	rec, ok := op.Step(RestoreStepStage)
	if !ok {
		return stageResult{}, StagingArea{}, durable.Permanent(fmt.Errorf("restore has no stage result: %w",
			ErrCorrupt))
	}

	var out stageResult

	decoder := json.NewDecoder(bytes.NewReader(rec.Result))
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(&out); err != nil {
		return stageResult{}, StagingArea{}, durable.Permanent(fmt.Errorf("decode the stage result: %w", ErrCorrupt))
	}

	area, err := ParseStagingArea(out.Area)
	if err != nil {
		return stageResult{}, StagingArea{}, durable.Permanent(err)
	}

	return out, area, nil
}

// restoreStepFunc is the body of one restore step, with the decoded input.
type restoreStepFunc func(ctx context.Context, sc durable.StepContext, in restoreInput) ([]byte, error)

// steps returns the steps of the restore operation, in order.
func (s *RestoreService) steps() []durable.Step {
	bodies := map[string]restoreStepFunc{
		RestoreStepStage:            s.stepStage,
		RestoreStepCheck:            s.stepCheck,
		RestoreStepEnterMaintenance: s.stepEnter,
		RestoreStepPreBackup:        s.stepPreBackup,
		RestoreStepCopyBlobs:        s.stepCopyBlobs,
		RestoreStepSwap:             s.stepSwap,
		RestoreStepAfterApply:       s.stepAfterApply,
		RestoreStepLeaveMaintenance: s.stepLeave,
		RestoreStepCleanup:          s.stepCleanup,
		RestoreStepFinish:           s.stepFinish,
	}

	out := make([]durable.Step, 0, len(bodies))

	for _, name := range RestoreSteps() {
		out = append(out, durable.Step{Name: name, Run: s.guarded(bodies[name])})
	}

	return out
}

// guarded runs body with the decoded input and maps its error. A stop of
// the claim or of the context returns as it is, so the runner stops the
// attempt. A domain write that finds its step recorded is done. An error that
// no retry can fix, and any error of the last attempt, ends the restore failed.
func (s *RestoreService) guarded(body restoreStepFunc) durable.StepFunc {
	return func(ctx context.Context, sc durable.StepContext) ([]byte, error) {
		in, err := readRestoreInput(sc.Operation)
		if err != nil {
			return nil, err
		}

		out, err := body(ctx, sc, in)

		switch {
		case err == nil:
			return out, nil
		case ctx.Err() != nil, errors.Is(err, durable.ErrClaimLost), errors.Is(err, durable.ErrCanceled):
			return nil, err
		case errors.Is(err, durable.ErrStepDone):
			// A takeover or a retry found the domain write of this step
			// committed. The runner reads the stored result.
			return nil, nil
		case permanentRestoreError(err) || sc.Operation.Failures+1 >= durable.DefaultMaxAttempts:
			return nil, s.failRestore(ctx, sc, in, err)
		default:
			return nil, err
		}
	}
}

// permanentRestoreError reports whether err is a failure that no retry of
// the step can fix: the backup, the request or the live data refuse the
// restore.
func permanentRestoreError(err error) bool {
	if missing, ok := errors.AsType[*MissingObjectError](err); ok && missing != nil {
		return true
	}

	if unbacked, ok := errors.AsType[*BlobsNotBackedError](err); ok && unbacked != nil {
		return true
	}

	for _, sentinel := range []error{
		ErrCorrupt, ErrSchemaMismatch, ErrSchemaBeforeBaseline, ErrNotFound, ErrInvalidID, ErrFailed,
		ErrDevBackupRefused, ErrDevStripIncomplete, ErrRestoreAccessMismatch, ErrManifestKindInvalid,
		ErrInvalidRestoreID, ErrKeyUnchecked, maintenance.ErrActiveWork, secret.ErrKeyNotConfigured,
		secret.ErrWrapperMismatch, secret.ErrDecrypt,
	} {
		if errors.Is(err, sentinel) {
			return true
		}
	}

	return false
}

// failRestore ends the restore of sc failed with the stable text of cause,
// under the fence of the finish step, and leaves the maintenance mode that it
// entered unless the hold of the server keeps it. It returns the error that
// ends the operation: cause as a permanent error, or the error of the write
// when the claim is lost or the operation is canceled, which the runner reads
// as a stop.
func (s *RestoreService) failRestore(ctx context.Context, sc durable.StepContext, in restoreInput, cause error) error {
	held, err := s.maintenance.Held(ctx)
	if err != nil {
		// A hold that cannot be read keeps maintenance mode: fail closed.
		held = true
	}

	text := sanitizeRestoreError(cause)
	end := RestoreEnd{
		ID: in.ID, State: RestoreFailed, Error: text, EndedAt: s.now().UTC(), DevReport: nil, LeaveMaintenance: !held,
	}

	s.cleanAfterFailure(ctx, sc, in)

	fence := sc.Operation.Claim.Step(RestoreStepFinish)

	_, finishErr := s.live.FinishRestore(ctx, fence, end)

	switch {
	case finishErr == nil, errors.Is(finishErr, durable.ErrStepDone), errors.Is(finishErr, ErrRestoreNotRunning):
		s.logRestore(ctx, restoreOutcome{
			id: in.ID, req: in.request(), state: RestoreFailed, errText: text,
			full: cause,
		})

		return durable.Permanent(cause)
	case errors.Is(finishErr, durable.ErrClaimLost), errors.Is(finishErr, durable.ErrCanceled):
		return finishErr
	default:
		return durable.Permanent(errors.Join(cause, fmt.Errorf("mark restore %s failed: %w", in.ID, finishErr)))
	}
}

// cleanAfterFailure deletes the temporary blob keys and drops the staging
// areas of a restore that fails, under the fence of the cleanup step, so that
// the staged data of a refused backup does not stay until the next restore.
// It does not return its error: the cleanup of the next restore drops what
// stays.
func (s *RestoreService) cleanAfterFailure(ctx context.Context, sc durable.StepContext, in restoreInput) {
	if err := s.deleteTempKeys(ctx, in.ID); err != nil {
		s.logger.WarnContext(ctx, "temporary restore blobs of a failed restore were not deleted",
			slog.String("restore_id", in.ID.String()))
	}

	fence := sc.Operation.Claim.Step(RestoreStepCleanup)
	if _, err := s.live.CleanRestoreAreas(ctx, fence, in.ID, s.lockWait); err != nil &&
		!errors.Is(err, durable.ErrStepDone) {
		s.logger.WarnContext(ctx, "staging areas of a failed restore were not dropped",
			slog.String("restore_id", in.ID.String()))
	}
}

// request returns the request of in, for the log line of the restore.
func (in restoreInput) request() RestoreRequest {
	return RestoreRequest{
		BackupID: in.BackupID, PreBackupID: in.PreBackupID, CheckOnly: in.CheckOnly, Force: in.Force,
		RequestedBy: in.RequestedBy, KeepAccessTables: in.KeepAccessTables,
	}
}

// stepStage pins the manifest, checks the kinds, verifies the SQL part, and
// stages it into the area of this claim token, with the dev data strip on a
// production installation. Its result names the area for every later step.
func (s *RestoreService) stepStage(ctx context.Context, sc durable.StepContext, in restoreInput) ([]byte, error) {
	pinned, err := s.pinManifest(ctx, in.BackupID)
	if err != nil {
		return nil, err
	}

	// Layer 1: compare the kind of the backup with the kind of the live
	// installation before anything stages and before maintenance mode.
	dev, err := s.devKinds(ctx, pinned)
	if err != nil {
		return nil, err
	}

	area, stage, err := s.stageInput(ctx, in, sc.Fence.Token, pinned, dev)
	if err != nil {
		return nil, err
	}

	_, report, err := s.live.StageRestore(ctx, area, stage)
	if err != nil {
		return nil, fmt.Errorf("stage backup %s for restore %s: %w", in.BackupID, in.ID, err)
	}

	if stage.Strip {
		dev.report = report
	}

	if !dev.report.Empty() && !s.allowDev {
		return nil, &devRefusalError{text: refusalSentence(reasonDevData, s.devHint)}
	}

	if stage.StripMode == DevStripRemove {
		s.logRemoval(ctx, dev.report)
	}

	dev.checkOnly = in.CheckOnly
	dev.report.Warnings = dev.warnings()

	out, err := json.Marshal(stageResult{
		Area: area.String(), SQLSize: pinned.sqlSize, SQLSHA: hex.EncodeToString([]byte(pinned.sqlSHA)),
		Schema:  pinned.schema,
		Dialect: pinned.dialect, Kind: pinned.kind, Wrappers: pinned.wrappers, Live: dev.live, Report: dev.report,
	})
	if err != nil {
		return nil, fmt.Errorf("encode the stage result of restore %s: %w", in.ID, err)
	}

	return out, nil
}

// stageInput verifies the SQL part of the backup, and returns the staging
// area of the claim token and the input of the stage.
func (s *RestoreService) stageInput(
	ctx context.Context, in restoreInput, token int64, pinned pinnedManifest, dev *devRun,
) (StagingArea, RestoreStageInput, error) {
	var none RestoreStageInput

	manifest, err := readManifest(ctx, s.target, in.BackupID)
	if err != nil {
		return StagingArea{Restore: "", Token: 0}, none, err
	}

	if verifyErr := verifySQLPart(ctx, s.target, in.BackupID, manifest.SQLFiles); verifyErr != nil {
		return StagingArea{Restore: "", Token: 0}, none, verifyErr
	}

	wantBlobs, err := parseManifestBlobs(manifest, in.BackupID)
	if err != nil {
		return StagingArea{Restore: "", Token: 0}, none, err
	}

	area, err := NewStagingArea(in.ID, token)
	if err != nil {
		return StagingArea{Restore: "", Token: 0}, none, err
	}

	// Layer 2: the staged data decides, not the kind in the manifest. The
	// strip runs in the area of this token only, before its record. A
	// check-only restore, and a production installation without the dev
	// setting, only detect.
	stage := RestoreStageInput{
		Open: portableOpener(ctx, s.target, in.BackupID), Want: restoreWant(manifest, wantBlobs),
		Files: manifest.SQLFiles, Options: StageOptions{BackupKind: pinned.kind, AllowDev: s.allowDev},
		Strip: dev.production(), StripMode: DevStripDetect,
	}

	if dev.production() && s.allowDev && !in.CheckOnly {
		stage.StripMode = DevStripRemove
	}

	return area, stage, nil
}

// stepCheck checks the key and the blobs of the backup against the pinned
// manifest, and stores the dev data report on the restore row. It only reads
// the staging area.
func (s *RestoreService) stepCheck(ctx context.Context, sc durable.StepContext, in restoreInput) ([]byte, error) {
	st, _, err := stageResultOf(sc.Operation)
	if err != nil {
		return nil, err
	}

	if repinErr := s.repinManifest(ctx, in.BackupID, st.pinned()); repinErr != nil {
		return nil, repinErr
	}

	manifest, err := readManifest(ctx, s.target, in.BackupID)
	if err != nil {
		return nil, err
	}

	check := backupKeyCheck{target: s.target, peeker: s.live, id: in.BackupID, manifest: manifest, key: s.key}
	if err := check.run(ctx); err != nil {
		return nil, err
	}

	if err := verifyTargetBlobs(ctx, s.target, manifest.Blobs); err != nil {
		return nil, err
	}

	if err := s.live.RecordRestoreReport(ctx, sc.Fence, in.ID, st.Report); err != nil {
		return nil, fmt.Errorf("record the dev data report of restore %s: %w", in.ID, err)
	}

	return nil, nil
}

// stepEnter enters maintenance mode under the claim, and confirms it. A
// takeover after an enter of this restore that it did not confirm lists the
// active work again instead.
func (s *RestoreService) stepEnter(ctx context.Context, sc durable.StepContext, in restoreInput) ([]byte, error) {
	if in.CheckOnly {
		return nil, nil
	}

	entered, err := s.live.RestoreMaintenanceEntered(ctx, in.ID)
	if err != nil {
		return nil, fmt.Errorf("read the maintenance flag of restore %s: %w", in.ID, err)
	}

	opts := maintenance.EnterOptions{Force: in.Force, Writer: s.writer(sc.Fence, in.ID)}

	var res maintenance.EnterResult

	if entered {
		res, err = s.maintenance.RecheckWith(ctx, opts)
	} else {
		res, err = s.maintenance.EnterWith(ctx, opts)
	}

	canceled := canceledOf(res, err)
	s.auditCanceled(ctx, in, sc.Fence, canceled)

	if err != nil {
		return nil, fmt.Errorf("enter maintenance mode for restore %s: %w", in.ID, err)
	}

	_, err = s.live.ConfirmRestoreMaintenance(ctx, sc.Fence, in.ID, maintenance.StateMaintenance, canceled)
	if err != nil {
		return nil, fmt.Errorf("confirm maintenance mode for restore %s: %w", in.ID, err)
	}

	return nil, nil
}

// canceledOf returns the units that an enter canceled, also when it failed.
func canceledOf(res maintenance.EnterResult, err error) []string {
	if active, ok := errors.AsType[*maintenance.ActiveWorkError](err); ok && active != nil {
		return active.Canceled
	}

	if canceled, ok := errors.AsType[*maintenance.CanceledError](err); ok && canceled != nil {
		return canceled.Canceled
	}

	return res.Canceled
}

// stepPreBackup takes the backup of the live data as its own durable
// operation, waits for it, and moves the restore to applying with its ID. A
// takeover waits for the same backup.
func (s *RestoreService) stepPreBackup(ctx context.Context, sc durable.StepContext, in restoreInput) ([]byte, error) {
	if in.CheckOnly {
		return nil, nil
	}

	if err := s.backups.Start(ctx, in.PreBackupID); err != nil && !errors.Is(err, ErrAlreadyExists) {
		return nil, fmt.Errorf("start the backup of the live data before restore %s: %w", in.ID, err)
	}

	pre, err := s.backups.Await(ctx, in.PreBackupID)
	if err != nil {
		return nil, fmt.Errorf("backup live data before restore %s: %w", in.ID, err)
	}

	if err := s.live.MarkRestoreApplying(ctx, sc.Fence, in.ID, pre.ID); err != nil {
		return nil, fmt.Errorf("mark restore %s applying: %w", in.ID, err)
	}

	return nil, nil
}

// stepCopyBlobs pins the manifest again and copies each blob that it names
// into the live store with checked bytes.
func (s *RestoreService) stepCopyBlobs(ctx context.Context, sc durable.StepContext, in restoreInput) ([]byte, error) {
	if in.CheckOnly {
		return nil, nil
	}

	st, _, err := stageResultOf(sc.Operation)
	if err != nil {
		return nil, err
	}

	if repinErr := s.repinManifest(ctx, in.BackupID, st.pinned()); repinErr != nil {
		return nil, repinErr
	}

	manifest, err := readManifest(ctx, s.target, in.BackupID)
	if err != nil {
		return nil, err
	}

	var copied copyBlobsResult

	for _, ref := range manifest.Blobs {
		repaired, copyErr := s.copyBlob(ctx, in, sc.Fence.Token, ref)
		if copyErr != nil {
			return nil, fmt.Errorf("copy blobs for restore %s: %w", in.ID, copyErr)
		}

		if repaired {
			copied.Repaired = append(copied.Repaired, ref.Key)
		}
	}

	out, err := json.Marshal(copied)
	if err != nil {
		return nil, fmt.Errorf("encode the copy result of restore %s: %w", in.ID, err)
	}

	return out, nil
}

// copyBlobsResult is the result of the copy-blobs step: the live keys whose
// bytes differed from the backup and that the step overwrote. The swap
// replaces the audit log, so the after-apply step writes their audit entries
// again from this result.
type copyBlobsResult struct {
	Repaired []string `json:"repaired"`
}

// repairedOf returns the keys that the copy-blobs step of op repaired. A
// result that does not decode is ErrCorrupt.
func repairedOf(op durable.Operation) ([]string, error) {
	rec, ok := op.Step(RestoreStepCopyBlobs)
	if !ok || len(rec.Result) == 0 {
		return nil, nil
	}

	var r copyBlobsResult

	decoder := json.NewDecoder(bytes.NewReader(rec.Result))
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(&r); err != nil {
		return nil, fmt.Errorf("%w: the result of the copy-blobs step does not decode", ErrCorrupt)
	}

	for _, key := range r.Repaired {
		// Defense in depth: the step wrote only keys that blob.ParseKey
		// accepted; this check stays so that no other text reaches an entry.
		if _, err := blob.ParseKey(key); err != nil {
			return nil, fmt.Errorf("%w: the copy-blobs result names a key that is not valid", ErrCorrupt)
		}
	}

	return r.Repaired, nil
}

// stepSwap swaps the staging area of the stage result into the live data.
// The swap transaction checks the fence and records the step.
func (s *RestoreService) stepSwap(ctx context.Context, sc durable.StepContext, in restoreInput) ([]byte, error) {
	if in.CheckOnly {
		return nil, nil
	}

	_, area, err := stageResultOf(sc.Operation)
	if err != nil {
		return nil, err
	}

	if _, err := s.live.ApplyRestoreArea(ctx, sc.Fence, area, in.KeepAccessTables); err != nil {
		return nil, fmt.Errorf("apply staged data for restore %s: %w", in.ID, err)
	}

	return nil, nil
}

// stepAfterApply runs the after-apply hook, checks that each blob that the
// restored data names exists, and appends the audit entries of the restore
// in one fenced write, so they commit once.
func (s *RestoreService) stepAfterApply(ctx context.Context, sc durable.StepContext, in restoreInput) ([]byte, error) {
	if in.CheckOnly {
		return nil, nil
	}

	if err := s.afterApplyChecks(ctx, sc, in); err != nil {
		return nil, err
	}

	entries, err := s.afterApplyEntries(sc, in)
	if err != nil {
		return nil, err
	}

	_, err = s.live.AppendRestoreAudit(ctx, sc.Fence, in.ID, entries)

	switch {
	case err == nil:
		return nil, nil
	case ctx.Err() != nil, errors.Is(err, durable.ErrClaimLost), errors.Is(err, durable.ErrCanceled),
		errors.Is(err, durable.ErrStepDone):
		return nil, err
	case sc.Operation.Failures+1 < durable.DefaultMaxAttempts:
		return nil, fmt.Errorf("append the audit entries of restore %s: %w", in.ID, err)
	default:
		// The swap already happened, and the report is in the restore
		// record. The restore ends finished with a warning, and the error
		// goes to the log.
		s.logger.ErrorContext(ctx, "audit append failed", slog.String("restore_id", in.ID.String()))

		return encodeAfterApply(afterApplyResult{AuditFailed: true}), nil
	}
}

// afterApplyChecks runs the after-apply hook and checks that each blob that
// the restored data names exists.
func (s *RestoreService) afterApplyChecks(ctx context.Context, sc durable.StepContext, in restoreInput) error {
	if s.afterApply != nil {
		if err := s.afterApply(ctx); err != nil {
			return fmt.Errorf("after the apply of restore %s: %w", in.ID, err)
		}
	}

	manifest, err := readManifest(ctx, s.target, in.BackupID)
	if err != nil {
		return err
	}

	return s.checkNamedBlobs(ctx, in, sc.Fence.Token, manifest.Blobs)
}

// afterApplyEntries returns the audit entries of the restore: its own entry,
// the entries of the work that the enter canceled and of the repaired live
// keys, which the swap removed with the old audit log, and the entries of the
// dev data report.
func (s *RestoreService) afterApplyEntries(sc durable.StepContext, in restoreInput) ([]audit.Entry, error) {
	st, _, err := stageResultOf(sc.Operation)
	if err != nil {
		return nil, err
	}

	entries := []audit.Entry{s.restoreAuditEntry(in, s.restoreAuditObject(in))}

	if rec, ok := sc.Operation.Step(RestoreStepEnterMaintenance); ok {
		enter, decodeErr := DecodeRestoreMaintenance(rec.Result)
		if decodeErr != nil {
			return nil, durable.Permanent(decodeErr)
		}

		if len(enter.Canceled) > 0 {
			entries = append(entries, s.restoreAuditEntry(in, canceledAuditObject(in.ID, enter.Token, enter.Canceled)))
		}
	}

	repaired, err := repairedOf(sc.Operation)
	if err != nil {
		return nil, durable.Permanent(err)
	}

	for _, key := range repaired {
		entries = append(entries, s.restoreAuditEntry(in, repairAuditObject(in, key)))
	}

	for _, object := range devAuditObjects(in.ID, in.BackupID, st.Report) {
		entries = append(entries, s.restoreAuditEntry(in, object))
	}

	return entries, nil
}

// afterApplyResult is the result of the after-apply step.
type afterApplyResult struct {
	// AuditFailed is true when the audit entries of the restore were not
	// written on the last attempt of the step.
	AuditFailed bool `json:"auditFailed"`
}

// encodeAfterApply encodes r as the result of the after-apply step.
func encodeAfterApply(r afterApplyResult) []byte {
	if r.AuditFailed {
		return []byte(`{"auditFailed":true}`)
	}

	return []byte(`{"auditFailed":false}`)
}

// auditFailedOf reports whether the after-apply step of op recorded that the
// audit entries were not written. A result that does not decode reports a
// failure: the warning then shows instead of a silent gap.
func auditFailedOf(op durable.Operation) bool {
	rec, ok := op.Step(RestoreStepAfterApply)
	if !ok || len(rec.Result) == 0 {
		return false
	}

	var r afterApplyResult
	if err := json.Unmarshal(rec.Result, &r); err != nil {
		return true
	}

	return r.AuditFailed
}

// stepLeave leaves the maintenance mode that this restore entered, and
// confirms the state. The hold of the server keeps maintenance mode.
func (s *RestoreService) stepLeave(ctx context.Context, sc durable.StepContext, in restoreInput) ([]byte, error) {
	if in.CheckOnly {
		return nil, nil
	}

	rec, ok := sc.Operation.Step(RestoreStepEnterMaintenance)
	if !ok {
		return nil, durable.Permanent(fmt.Errorf("restore %s has no enter result: %w", in.ID, ErrCorrupt))
	}

	enter, err := DecodeRestoreMaintenance(rec.Result)
	if err != nil {
		return nil, durable.Permanent(err)
	}

	want, err := s.leave(ctx, sc.Fence, in.ID, enter)
	if err != nil {
		return nil, err
	}

	if _, err := s.live.ConfirmRestoreMaintenance(ctx, sc.Fence, in.ID, want, nil); err != nil {
		return nil, fmt.Errorf("confirm the leave of maintenance mode for restore %s: %w", in.ID, err)
	}

	return nil, nil
}

// leave leaves maintenance mode when this restore entered it, and returns
// the state that the leave step confirms.
func (s *RestoreService) leave(
	ctx context.Context, fence durable.StepFence, id RestoreID, enter RestoreMaintenanceResult,
) (maintenance.State, error) {
	if !enter.Entered {
		// The maintenance mode of an operator stays.
		state, err := s.maintenance.Get(ctx)
		if err != nil {
			return "", fmt.Errorf("read the installation state for restore %s: %w", id, err)
		}

		return state, nil
	}

	err := s.maintenance.LeaveWith(ctx, s.writer(fence, id))

	switch {
	case err == nil:
		return maintenance.StateNormal, nil
	case errors.Is(err, maintenance.ErrHeld):
		return maintenance.StateMaintenance, nil
	default:
		return "", fmt.Errorf("leave maintenance mode after restore %s: %w", id, err)
	}
}

// stepCleanup deletes the temporary blob keys of this restore and of each
// restore that ended, then drops the old live data and the staging areas.
func (s *RestoreService) stepCleanup(ctx context.Context, sc durable.StepContext, in restoreInput) ([]byte, error) {
	if err := s.deleteTempKeys(ctx, in.ID); err != nil {
		return nil, err
	}

	if _, err := s.live.CleanRestoreAreas(ctx, sc.Fence, in.ID, s.lockWait); err != nil {
		return nil, fmt.Errorf("clean the areas of restore %s: %w", in.ID, err)
	}

	return nil, nil
}

// stepFinish ends the restore row succeeded with the final dev report.
func (s *RestoreService) stepFinish(ctx context.Context, sc durable.StepContext, in restoreInput) ([]byte, error) {
	st, _, err := stageResultOf(sc.Operation)
	if err != nil {
		return nil, err
	}

	report := st.Report

	if rec, ok := sc.Operation.Step(RestoreStepSwap); ok && len(rec.Result) > 0 {
		kept, err := DecodeRestoreSwap(rec.Result)
		if err != nil {
			return nil, durable.Permanent(err)
		}

		if kept.DroppedTempAdmins > 0 {
			report.Classes = withClass(report.Classes, RemovedTempAdmins, kept.DroppedTempAdmins)
		}
	}

	if auditFailedOf(sc.Operation) {
		report.Warnings = append(slices.Clone(report.Warnings), WarnAuditAppendFailed)
	}

	end := RestoreEnd{
		ID: in.ID, State: RestoreSucceeded, Error: "", EndedAt: s.now().UTC(), DevReport: &report,
		LeaveMaintenance: false,
	}

	if _, err := s.live.FinishRestore(ctx, sc.Fence, end); err != nil {
		return nil, fmt.Errorf("finish restore %s: %w", in.ID, err)
	}

	s.logRestore(ctx, restoreOutcome{id: in.ID, req: in.request(), state: RestoreSucceeded, errText: "", full: nil})

	return nil, nil
}

// withClass returns classes with n added to class.
func withClass(classes map[DevRemovalClass]int64, class DevRemovalClass, n int64) map[DevRemovalClass]int64 {
	out := make(map[DevRemovalClass]int64, len(classes)+1)
	maps.Copy(out, classes)

	out[class] += n

	return out
}

// restoreWriter is the maintenance.StateWriter of one restore step: each
// state write checks the fence of the step in its transaction.
type restoreWriter struct {
	s     *RestoreService
	fence durable.StepFence
	id    RestoreID
}

// writer returns the state writer of the step of fence.
func (s *RestoreService) writer(fence durable.StepFence, id RestoreID) restoreWriter {
	return restoreWriter{s: s, fence: fence, id: id}
}

// SetStateIf implements maintenance.StateWriter.
func (w restoreWriter) SetStateIf(ctx context.Context, expected, next maintenance.State) error {
	if err := w.s.live.SetRestoreMaintenance(ctx, w.fence, w.id, expected, next); err != nil {
		return fmt.Errorf("set the installation state for restore %s: %w", w.id, err)
	}

	return nil
}

// Check implements maintenance.StateWriter: it reads the operation again and
// reports durable.ErrClaimLost unless the claim of the fence still holds it,
// with no cancel and no record of the step.
func (w restoreWriter) Check(ctx context.Context) error {
	op, err := w.s.operations.Get(ctx, w.fence.Operation)
	if err != nil {
		return fmt.Errorf("read the operation of restore %s: %w", w.id, err)
	}

	if _, done := op.Step(w.fence.Step); op.State != durable.StateRunning || op.Claim.Token != w.fence.Token ||
		op.CancelRequested || done {
		return fmt.Errorf("restore %s: %w", w.id, durable.ErrClaimLost)
	}

	return nil
}

// Actor implements maintenance.StateWriter.
func (w restoreWriter) Actor() string {
	return "restore " + w.id.String() + " claim token " + strconv.FormatInt(w.fence.Token, 10)
}

// auditCanceled writes one audit entry for the work that an enter of the
// restore canceled, with the restore ID and the claim token. A lost entry
// does not fail the restore: the log line keeps the record.
func (s *RestoreService) auditCanceled(ctx context.Context, in restoreInput, fence durable.StepFence, units []string) {
	if len(units) == 0 {
		return
	}

	object := canceledAuditObject(in.ID, fence.Token, units)

	s.logger.WarnContext(ctx, "restore canceled the active work", slog.String("restore_id", in.ID.String()),
		slog.Int64("token", fence.Token), slog.Int("units", len(units)))

	if store, ok := s.live.(serverAuditAppender); ok {
		if _, err := store.AppendServer(ctx, s.restoreAuditEntry(in, object)); err != nil {
			s.logger.ErrorContext(ctx, "audit append failed", slog.String("restore_id", in.ID.String()))
		}
	}
}

// repairAuditObject names the repair of the live key by restore in, for its
// audit entry.
func repairAuditObject(in restoreInput, key string) string {
	return "restore " + in.ID.String() + " repaired blob " + strconv.Quote(key) +
		" with the checked bytes of backup " + in.BackupID.String()
}

// canceledAuditObject names the work that the enter of restore id with the
// claim token canceled, for its audit entry.
func canceledAuditObject(id RestoreID, token int64, units []string) string {
	return "restore " + id.String() + " claim token " + strconv.FormatInt(token, 10) +
		" canceled the active work: " + strings.Join(units, ", ")
}

// restoreAuditObject names one restore for its audit entry, with whether
// the access tables were replaced or kept.
func (s *RestoreService) restoreAuditObject(in restoreInput) string {
	mode := "replaced"
	if in.KeepAccessTables {
		mode = "kept"
	}

	return "restore " + in.ID.String() + " of backup " + in.BackupID.String() + " (access tables " + mode +
		s.devFlagText() + ")"
}

// restoreAuditEntry returns an audit entry of the restore in with object.
func (s *RestoreService) restoreAuditEntry(in restoreInput, object string) audit.Entry {
	return audit.Entry{
		At:            s.now().UTC(),
		Namespace:     audit.ServerScope,
		Subject:       in.RequestedBy,
		IDPSubject:    "",
		TokenID:       "",
		Action:        string(identity.ActionServerRestore),
		Object:        object,
		Version:       "",
		ValuePath:     "",
		ValueCount:    nil,
		Changes:       nil,
		SourceIP:      "",
		UserAgent:     "",
		Channel:       audit.ChannelAPI,
		Kind:          audit.KindChange,
		Outcome:       audit.OutcomeSuccess,
		RequestID:     "",
		ObjectID:      "",
		PrevHash:      "",
		Hash:          "",
		Sequence:      0,
		FenceVersion:  0,
		Reason:        "",
		PrincipalKind: "",
		JobID:         "",
	}
}

// tempKey returns the temporary key of blob key for the restore id and the
// claim token.
func tempKey(id RestoreID, token int64, key string) (blob.Key, error) {
	// Defense in depth: the input check and NewStagingArea check id and
	// token; these checks stay so that no other value reaches a key.
	if _, err := NewStagingArea(id, token); err != nil {
		return "", err
	}

	tmp, err := blob.ParseKey(tempKeyPrefix + "/" + id.String() + "/" + strconv.FormatInt(token, 10) + "/" + key)
	if err != nil {
		return "", fmt.Errorf("temporary key of blob %q: %w", key, err)
	}

	return tmp, nil
}

// copyBlob copies one blob of the backup into the live store with checked
// bytes, with no conditional write and no delete of a live key:
//
//  1. A live key that holds the bytes of the manifest stays.
//  2. The bytes of the backup go to a temporary key of this restore and
//     token, with the check of the size and the checksum. A failed check
//     deletes only the temporary key.
//  3. The checked bytes go to the live key. A live key with other bytes is
//     a repair: the restore overwrites it, and writes a log line and an
//     audit entry. A store that refuses the overwrite fails the copy with
//     ErrCorrupt.
func (s *RestoreService) copyBlob(ctx context.Context, in restoreInput, token int64, ref ManifestBlob) (bool, error) {
	live, err := blob.ParseKey(ref.Key)
	if err != nil {
		return false, fmt.Errorf("restore blob key %q: %w", ref.Key, err)
	}

	verifyErr := s.verifyLiveBlob(ctx, ref)

	switch {
	case verifyErr == nil:
		return false, nil
	case isNotFound(verifyErr), errors.Is(verifyErr, ErrCorrupt):
	default:
		return false, verifyErr
	}

	tmp, err := s.stageTemp(ctx, in.ID, token, ref)
	if err != nil {
		return false, err
	}

	wrote, err := s.placeChecked(ctx, tmp, live, ref)
	if err != nil {
		return false, err
	}

	// A live key with other bytes that the copy overwrote is a repair.
	repaired := wrote && !isNotFound(verifyErr)
	if repaired {
		s.logRepair(ctx, in, ref)
	}

	// The temporary key has done its work. A failed delete leaves it to the
	// cleanup step.
	if delErr := s.blobs.Delete(ctx, tmp); delErr != nil {
		s.logger.WarnContext(ctx, "temporary restore blob was not deleted", slog.String("restore_id", in.ID.String()))
	}

	return repaired, nil
}

// stageTemp copies the object of ref from the backup to the temporary key of
// the restore id and the claim token, with the check of its size and its
// checksum, and returns the key. A failed check deletes only the temporary
// key. A live key is never deleted.
func (s *RestoreService) stageTemp(ctx context.Context, id RestoreID, token int64, ref ManifestBlob) (blob.Key, error) {
	tmp, err := tempKey(id, token, ref.Key)
	if err != nil {
		return "", err
	}

	// The temporary key belongs to this token only: a key that an earlier
	// try of the same claim left goes first.
	if clearErr := s.blobs.Delete(ctx, tmp); clearErr != nil {
		return "", fmt.Errorf("clear the temporary key of blob %q: %w", ref.Key, clearErr)
	}

	source, err := blob.ParseKey("blobs/" + ref.Key)
	if err != nil {
		return "", fmt.Errorf("restore blob key %q: %w", ref.Key, err)
	}

	if putErr := s.putChecked(ctx, copyFrom{store: s.target, key: source, target: true}, tmp, ref); putErr != nil {
		return "", errors.Join(putErr, s.blobs.Delete(ctx, tmp))
	}

	return tmp, nil
}

// placeChecked copies the checked bytes of the temporary key tmp to the live
// key. It reports whether it wrote the live key. A store with conditional
// writes refuses the overwrite: the live key must then hold the bytes of the
// manifest, or the copy fails with ErrCorrupt.
func (s *RestoreService) placeChecked(ctx context.Context, tmp, live blob.Key, ref ManifestBlob) (bool, error) {
	err := s.putChecked(ctx, copyFrom{store: s.blobs, key: tmp, target: false}, live, ref)

	switch {
	case err == nil:
		return true, nil
	case !errors.Is(err, blob.ErrExists):
		return false, err
	}

	if verifyErr := s.verifyLiveBlob(ctx, ref); verifyErr != nil {
		return false, errors.Join(verifyErr, ErrCorrupt)
	}

	return false, nil
}

// copyFrom names the source of one copy: a store, a key in it, and whether
// the store is the backup target, where a missing object is an incomplete
// backup.
type copyFrom struct {
	store  blob.Store
	key    blob.Key
	target bool
}

// putChecked puts the object of from under dst of the live store while it
// checks the size and the MD5 of ref. It reports ErrCorrupt on a mismatch
// and deletes nothing: the caller deletes a temporary key.
func (s *RestoreService) putChecked(ctx context.Context, from copyFrom, dst blob.Key, ref ManifestBlob) error {
	check, err := newObjectCheck(ref.Key, blobWant(ref))
	if err != nil {
		return err
	}

	reader, err := from.store.Get(ctx, from.key)
	if err != nil {
		if isNotFound(err) && from.target {
			return &MissingObjectError{Key: from.key}
		}

		return fmt.Errorf("read blob %q: %w", ref.Key, err)
	}

	putErr := s.blobs.Put(ctx, dst, io.TeeReader(reader, check))
	closeErr := reader.Close()

	if putErr != nil {
		return fmt.Errorf("copy blob %q: %w", ref.Key, putErr)
	}

	if closeErr != nil {
		return fmt.Errorf("close blob %q: %w", ref.Key, closeErr)
	}

	return check.verify(ref.Key)
}

// logRepair writes the log line and the audit entry of a live key whose
// bytes differed from the manifest, which the restore overwrote with the
// checked bytes of the backup.
func (s *RestoreService) logRepair(ctx context.Context, in restoreInput, ref ManifestBlob) {
	s.logger.WarnContext(ctx, "restore overwrote a live blob whose bytes differed from the backup",
		slog.String("restore_id", in.ID.String()), slog.String("blob", ref.Key))

	if store, ok := s.live.(serverAuditAppender); ok {
		if _, err := store.AppendServer(ctx, s.restoreAuditEntry(in, repairAuditObject(in, ref.Key))); err != nil {
			s.logger.ErrorContext(ctx, "audit append failed", slog.String("restore_id", in.ID.String()))
		}
	}
}

// checkNamedBlobs checks that the live store holds each blob that the
// restored data names, and copies a missing one again from the backup. A
// deleter that stalled across the restore can have deleted one.
func (s *RestoreService) checkNamedBlobs(ctx context.Context, in restoreInput, token int64, refs []ManifestBlob) error {
	for _, ref := range refs {
		if _, err := s.copyBlob(ctx, in, token, ref); err != nil {
			return fmt.Errorf("check the blobs of restore %s: %w", in.ID, err)
		}
	}

	return nil
}

// deleteTempKeys deletes the temporary blob keys of restore id and of each
// restore whose row ended. The keys of a restore with no row stay: another
// installation can share the store.
func (s *RestoreService) deleteTempKeys(ctx context.Context, id RestoreID) error {
	objects, err := s.blobs.List(ctx, tempKeyPrefix)
	if err != nil {
		return fmt.Errorf("list the temporary blobs of restore %s: %w", id, err)
	}

	ended := map[RestoreID]bool{id: true}

	for _, obj := range objects {
		key := obj.Key.String()

		// Defense in depth: List filters by the prefix, but a store whose
		// List matches a plain string prefix returns other keys too. This
		// check stays so that the cleanup never deletes a live key.
		rest, ok := strings.CutPrefix(key, tempKeyPrefix+"/")
		if !ok {
			continue
		}

		owner, _, ok := strings.Cut(rest, "/")
		if !ok {
			continue
		}

		restore, err := ParseRestoreID(owner)
		if err != nil {
			continue
		}

		if _, known := ended[restore]; !known {
			ended[restore] = s.restoreEnded(ctx, restore)
		}

		if !ended[restore] {
			continue
		}

		if err := s.blobs.Delete(ctx, obj.Key); err != nil {
			return fmt.Errorf("delete a temporary blob of restore %s: %w", restore, err)
		}
	}

	return nil
}

// restoreEnded reports whether the row of restore id exists and ended. A
// row that cannot be read counts as running: its keys stay.
func (s *RestoreService) restoreEnded(ctx context.Context, id RestoreID) bool {
	record, err := s.live.GetRestore(ctx, id)
	if err != nil {
		return false
	}

	return record.State == RestoreSucceeded || record.State == RestoreFailed
}

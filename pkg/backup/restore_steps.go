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
	"sync"

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

// restoreCopyConcurrency is the count of blobs that one restore copies at the
// same time.
const restoreCopyConcurrency = 8

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
	Digest   string            `json:"manifestSha256"`
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
		wrappers: r.Wrappers, digest: r.Digest,
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
		ErrInvalidRestoreID, ErrKeyUnchecked, ErrPreBackupForeign, maintenance.ErrActiveWork, secret.ErrKeyNotConfigured,
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
// entered unless the hold of the server keeps it. After the swap it first
// writes the audit entries of the restore under the fence of the after-apply
// step, as that step does: the swap replaced the audit log. Before the swap
// it writes the audit events of the restore after the end, into the audit
// log that the restore did not change. It returns the error that ends the
// operation: cause as a permanent error, or the error of the write when the
// claim is lost or the operation is canceled, which the runner reads as a
// stop.
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

	if code := restoreErrorCode(cause); code != "" {
		end.DevReport = refusedReport(sc.Operation, cause, code)
	}

	_, swapped := sc.Operation.Step(RestoreStepSwap)
	if swapped {
		if stop := s.auditAfterSwap(ctx, sc, in, &end); stop != nil {
			return stop
		}
	}

	s.cleanAfterFailure(ctx, sc, in)

	fence := sc.Operation.Claim.Step(RestoreStepFinish)

	_, finishErr := s.live.FinishRestore(ctx, fence, end)

	switch {
	case finishErr == nil, errors.Is(finishErr, durable.ErrStepDone), errors.Is(finishErr, ErrRestoreNotRunning):
		if !swapped && finishErr == nil {
			s.appendEvents(ctx, in, restoreEndFailed)
		}

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

// refusedReport returns the final report of a restore of op that a refusal
// with code stopped: the report of the stage step, with code and, for a stop
// at the live blobs, the findings of the backup before the restore.
func refusedReport(op durable.Operation, cause error, code RestoreErrorCode) *DevStripReport {
	report := emptyDevReport()
	if st, _, err := stageResultOf(op); err == nil {
		report = st.Report
	}

	report.ErrorCode = code

	if refused, ok := errors.AsType[*LiveBlobsError](cause); ok && refused != nil {
		found := refused.Findings
		found.Keys = slices.Clone(found.Keys)
		report.PreBackupFound = &found
	}

	return &report
}

// auditAfterSwap writes the audit entries of a restore that fails after its
// swap, under the fence of the after-apply step, unless that step recorded
// them. A failed append adds the warning audit-append-failed to end. It
// returns the error of a lost claim or a cancel, which stops the failure.
func (s *RestoreService) auditAfterSwap(
	ctx context.Context, sc durable.StepContext, in restoreInput, end *RestoreEnd,
) error {
	if _, done := sc.Operation.Step(RestoreStepAfterApply); done {
		return nil
	}

	entries, err := s.afterApplyEntries(ctx, sc.Operation, in, swappedFailed)
	if err == nil {
		_, err = s.live.AppendRestoreAudit(ctx, sc.Operation.Claim.Step(RestoreStepAfterApply), in.ID, entries)
	}

	switch {
	case err == nil, errors.Is(err, durable.ErrStepDone):
		return nil
	case errors.Is(err, durable.ErrClaimLost), errors.Is(err, durable.ErrCanceled):
		return err
	}

	s.logger.ErrorContext(ctx, "audit append failed", slog.String("restore_id", in.ID.String()))

	report := emptyDevReport()
	if st, _, stErr := stageResultOf(sc.Operation); stErr == nil {
		report = st.Report
	}

	report.Warnings = append(slices.Clone(report.Warnings), WarnAuditAppendFailed)
	end.DevReport = &report

	return nil
}

// swappedEnd says how a restore that swapped ended, for its audit entry.
type swappedEnd string

const (
	// swappedFinished is a restore that ran its after-apply step.
	swappedFinished swappedEnd = "finished"
	// swappedFailed is a restore that failed or stopped after its swap.
	swappedFailed swappedEnd = "failed"
)

// Values of the end of a restore before its swap, for its audit entry.
const (
	// restoreEndFailed is a restore that a failure stopped before its swap.
	restoreEndFailed = "failed"
	// restoreEndCanceled is a restore that a cancel stopped before its swap.
	restoreEndCanceled = "canceled"
	// restoreEndStopped is a restore that stopped before its swap with no
	// end write and no cancel.
	restoreEndStopped = "stopped"
)

// appendEvents writes the audit entries of the audit events of restore in
// into the audit log of the installation, for a restore that ended before
// its swap: the audit log is still the one from before the restore. Then it
// writes the entry of the end, with the outcome failure, which says how the
// restore ended and that the live data did not change. A lost
// entry does not change the end: the log line keeps the record.
func (s *RestoreService) appendEvents(ctx context.Context, in restoreInput, end string) {
	store, ok := s.live.(serverAuditAppender)
	if !ok {
		return
	}

	events, err := s.live.RestoreEvents(ctx, in.ID)
	if err != nil {
		s.logger.ErrorContext(ctx, "audit events of a restore were not read", slog.String("restore_id", in.ID.String()))

		return
	}

	for _, object := range events {
		if _, err := store.AppendServer(ctx, s.restoreAuditEntry(in, object)); err != nil {
			s.logger.ErrorContext(ctx, "audit append failed", slog.String("restore_id", in.ID.String()))
		}
	}

	last := s.restoreAuditEntry(in, endBeforeSwapAuditObject(in, end))
	last.Outcome = audit.OutcomeFailure

	if _, err := store.AppendServer(ctx, last); err != nil {
		s.logger.ErrorContext(ctx, "audit append failed", slog.String("restore_id", in.ID.String()))
	}
}

// cleanAfterFailure deletes the temporary blob keys of the claim token of sc
// and drops the staging areas of a restore that fails, under the fence of the
// cleanup step, so that the staged data of a refused backup does not stay
// until the next restore. It records the cleanup step before the finish
// step; the runner skips a recorded step. It deletes only the keys of its own
// token: a holder that lost its claim must not delete the keys of the live
// holder. It does not return its error: the cleanup of the next restore drops
// what stays.
func (s *RestoreService) cleanAfterFailure(ctx context.Context, sc durable.StepContext, in restoreInput) {
	if err := s.deleteOwnTempKeys(ctx, in.ID, sc.Operation.Claim.Token); err != nil {
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

// deleteOwnTempKeys deletes the temporary blob keys of restore id under the
// claim token.
func (s *RestoreService) deleteOwnTempKeys(ctx context.Context, id RestoreID, token int64) error {
	if _, err := NewStagingArea(id, token); err != nil {
		return err
	}

	prefix := tempKeyPrefix + "/" + id.String() + "/" + strconv.FormatInt(token, 10) + "/"

	objects, err := listObjects(ctx, s.blobs, prefix)
	if err != nil {
		return fmt.Errorf("list the temporary blobs of restore %s: %w", id, err)
	}

	for _, obj := range objects {
		// Defense in depth: List filters by the prefix, but a store whose
		// List matches a plain string prefix returns other keys too. This
		// check stays so that the cleanup never deletes a live key.
		if !strings.HasPrefix(obj.Key.String(), prefix) {
			continue
		}

		if err := s.blobs.Delete(ctx, obj.Key); err != nil {
			return fmt.Errorf("delete a temporary blob of restore %s: %w", id, err)
		}
	}

	return nil
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

	area, stage, err := s.stageInput(ctx, in, sc.Fence, pinned, dev)
	if err != nil {
		return nil, err
	}

	staged, report, err := s.live.StageRestore(ctx, area, stage)
	if err != nil {
		return nil, fmt.Errorf("stage backup %s for restore %s: %w", in.BackupID, in.ID, err)
	}

	// The rows of the staged data name blobs too. The stage compares them with
	// the manifest, which was checked above; this check stays so that a row
	// key does not depend on that comparison. Nothing is applied yet: the
	// staging area is dropped by the cleanup of the failed restore.
	if keyErr := s.refuseKeys(ctx, sc.Fence, in, manifestBlobs(staged.Blobs)); keyErr != nil {
		return nil, keyErr
	}

	if dropErr := s.dropIssuerKeys(ctx, sc, in, area); dropErr != nil {
		return nil, dropErr
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
	dev.report.NotHeld = dev.notHeld

	out, err := json.Marshal(stageResult{
		Area: area.String(), SQLSize: pinned.sqlSize, SQLSHA: hex.EncodeToString([]byte(pinned.sqlSHA)),
		Schema:  pinned.schema,
		Dialect: pinned.dialect, Kind: pinned.kind, Wrappers: pinned.wrappers, Digest: pinned.digest,
		Live: dev.live, Report: dev.report,
	})
	if err != nil {
		return nil, fmt.Errorf("encode the stage result of restore %s: %w", in.ID, err)
	}

	return out, nil
}

// dropIssuerKeys removes the issuer keys of the staged namespaces that belong
// to another installation, and records each drop. The decision is for each
// namespace, by the primary installation that the staged data names. A check
// only run records nothing.
func (s *RestoreService) dropIssuerKeys(
	ctx context.Context, sc durable.StepContext, in restoreInput, area StagingArea,
) error {
	dropped, err := s.live.DropStagedIssuerKeys(ctx, area, s.installation)
	if err != nil {
		return fmt.Errorf("drop the issuer keys of other installations for restore %s: %w", in.ID, err)
	}

	if in.CheckOnly {
		return nil
	}

	for _, d := range dropped {
		if recErr := s.recordKeysDropped(ctx, sc, in, d); recErr != nil {
			return recErr
		}
	}

	return nil
}

// stageInput verifies the SQL part of the backup, and returns the staging
// area of the claim token and the input of the stage.
func (s *RestoreService) stageInput(
	ctx context.Context, in restoreInput, fence durable.StepFence, pinned pinnedManifest, dev *devRun,
) (StagingArea, RestoreStageInput, error) {
	var none RestoreStageInput

	manifest, err := s.readPinnedManifest(ctx, in.BackupID, pinned)
	if err != nil {
		return StagingArea{Restore: "", Token: 0}, none, err
	}

	// A key that a restore does not write refuses the restore before the
	// first byte is staged or written.
	if keyErr := s.refuseKeys(ctx, fence, in, manifest.Blobs); keyErr != nil {
		return StagingArea{Restore: "", Token: 0}, none, keyErr
	}

	if verifyErr := verifySQLPart(ctx, s.target, in.BackupID, manifest.SQLFiles); verifyErr != nil {
		return StagingArea{Restore: "", Token: 0}, none, verifyErr
	}

	wantBlobs, err := restoreWantBlobs(manifest, in.BackupID)
	if err != nil {
		return StagingArea{Restore: "", Token: 0}, none, err
	}

	// A backup before an earlier restore holds no copy of the blobs that it
	// found missing or damaged. The restore leaves those keys as they are,
	// and its answer names them.
	if notHeld := findingsOf(manifest); !notHeld.Empty() {
		dev.notHeld = &notHeld
	}

	area, err := NewStagingArea(in.ID, fence.Token)
	if err != nil {
		return StagingArea{Restore: "", Token: 0}, none, err
	}

	// Layer 2: the staged data decides, not the kind in the manifest. The
	// strip runs in the area of this token only, before its record. A
	// check-only restore, and a production installation without the dev
	// setting, only detect.
	stage := RestoreStageInput{
		Open: portableOpener(ctx, s.target, in.BackupID), Want: restoreWant(manifest, wantBlobs),
		Files: manifest.SQLFiles, Options: StageOptions{
			BackupKind: pinned.kind, AllowDev: s.allowDev, RestoreAt: s.now().UTC(),
		},
		Strip: dev.production(), StripMode: DevStripDetect,
	}

	if dev.production() && s.allowDev && !in.CheckOnly {
		stage.StripMode = DevStripRemove
	}

	return area, stage, nil
}

// recordKeysDropped records, as an audit event of the restore and in the log,
// that the restore did not restore the signing keys of the issuer of one
// namespace.
func (s *RestoreService) recordKeysDropped(
	ctx context.Context, sc durable.StepContext, in restoreInput, d IssuerKeysDropped,
) error {
	object := fmt.Sprintf("restore %s did not restore the signing keys of the OIDC issuer of namespace %s "+
		"(%d keys): backup %s holds the namespace of another installation, so the namespace gets new keys",
		in.ID, d.Namespace, d.Keys, in.BackupID)

	if err := s.live.RecordRestoreEvent(ctx, sc.Fence, in.ID, object); err != nil {
		return fmt.Errorf("record the drop of the issuer keys for restore %s: %w", in.ID, err)
	}

	s.logger.WarnContext(ctx, "the restore does not restore the signing keys of the OIDC issuer of a namespace",
		slog.String("restore_id", in.ID.String()), slog.String("backup_id", in.BackupID.String()),
		slog.String("namespace", d.Namespace), slog.Int64("keys", d.Keys))

	return nil
}

// stepCheck checks the key and the blobs of the backup against the pinned
// manifest, and stores the dev data report on the restore row. It only reads
// the staging area.
func (s *RestoreService) stepCheck(ctx context.Context, sc durable.StepContext, in restoreInput) ([]byte, error) {
	st, _, err := stageResultOf(sc.Operation)
	if err != nil {
		return nil, err
	}

	manifest, err := s.readPinnedManifest(ctx, in.BackupID, st.pinned())
	if err != nil {
		return nil, err
	}

	// The worker checks the minimum binary version of the live schema too:
	// the start ran on another instance, and a binary below the minimum must
	// not swap in data that it staged. The swap checks it again in its own
	// transaction. The error is a retry, so a newer worker can take it.
	if checker, ok := s.live.(installation.MinimumChecker); ok {
		if err := checker.CheckBinaryMinimum(ctx); err != nil {
			return nil, fmt.Errorf("check the binary of restore %s: %w", in.ID, err)
		}
	}

	check := backupKeyCheck{target: s.target, peeker: s.live, id: in.BackupID, manifest: manifest, key: s.key}
	if err := check.run(ctx); err != nil {
		return nil, err
	}

	// Check the keys again: the check reads the target, which may have changed
	// since the stage. The refusal comes before any read of a blob.
	if keyErr := s.refuseKeys(ctx, sc.Fence, in, manifest.Blobs); keyErr != nil {
		return nil, keyErr
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

	opts := maintenance.EnterOptions{Force: in.Force, Writer: s.writer(sc.Fence, in.ID)}

	res, err := s.enterOrRecheck(ctx, in.ID, opts)

	canceled := canceledOf(res, err)
	s.logCanceled(ctx, in, sc.Fence, canceled)

	if err != nil {
		return nil, fmt.Errorf("enter maintenance mode for restore %s: %w", in.ID, err)
	}

	_, err = s.live.ConfirmRestoreMaintenance(ctx, sc.Fence, in.ID, maintenance.StateMaintenance, canceled)
	if err != nil {
		return nil, fmt.Errorf("confirm maintenance mode for restore %s: %w", in.ID, err)
	}

	return nil, nil
}

// enterOrRecheck enters maintenance mode, or, after a takeover that finds the
// flag of this restore set and the state maintenance, lists the active work
// again: work that raced the enter of the earlier holder can still run. A
// flag that is set while the state is normal means that an operator left
// maintenance mode after that enter, so the restore enters it again.
func (s *RestoreService) enterOrRecheck(
	ctx context.Context, id RestoreID, opts maintenance.EnterOptions,
) (maintenance.EnterResult, error) {
	entered, err := s.live.RestoreMaintenanceEntered(ctx, id)
	if err != nil {
		return maintenance.EnterResult{
			Canceled: nil,
		}, fmt.Errorf(
			"read the maintenance flag of restore %s: %w",
			id,
			err,
		)
	}

	if !entered {
		return s.maintenance.EnterWith(ctx, opts)
	}

	state, err := s.maintenance.Get(ctx)
	if err != nil {
		return maintenance.EnterResult{
			Canceled: nil,
		}, fmt.Errorf(
			"read the installation state for restore %s: %w",
			id,
			err,
		)
	}

	if state != maintenance.StateMaintenance {
		return s.maintenance.EnterWith(ctx, opts)
	}

	return s.maintenance.RecheckWith(ctx, opts)
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
// operation, runs it on this step, waits for it, and moves the restore to
// applying with its ID. The start claims the backup for this instance, so a
// backup worker never runs it while this step does. A takeover finds the
// backup started and waits for it with Await. When no instance holds a
// claim, Await runs a pending backup operation: the next one that the
// runner claims, not one that it picks by ID. That is this backup, because
// only one backup runs at a time.
//
// The backup records each live blob that it found missing or damaged, and
// this step stores one audit event of the restore with the counts. Then it
// stops the restore before any change of the live data when the backup found
// a damaged live blob, or more missing live blobs than the bound allows. A
// restore never writes over a live blob.
func (s *RestoreService) stepPreBackup(ctx context.Context, sc durable.StepContext, in restoreInput) ([]byte, error) {
	if in.CheckOnly {
		return nil, nil
	}

	if err := s.startPreBackup(ctx, sc.Fence, in); err != nil {
		return nil, err
	}

	pre, err := s.backups.Await(ctx, in.PreBackupID)
	if err != nil {
		return nil, fmt.Errorf("backup live data before restore %s: %w", in.ID, err)
	}

	manifest, err := readManifest(ctx, s.target, pre.ID)
	if err != nil {
		return nil, fmt.Errorf("read the backup of the live data before restore %s: %w", in.ID, err)
	}

	found := findingsOf(manifest)

	if recordErr := s.recordPreBackupFindings(ctx, sc.Fence, in, pre.ID, found); recordErr != nil {
		return nil, recordErr
	}

	if refused := checkLiveFindings(manifest, EffectiveMissingBlobLimit()); refused != nil {
		return nil, refused
	}

	out, err := json.Marshal(preBackupResult{Found: found})
	if err != nil {
		return nil, fmt.Errorf("encode the pre-backup result of restore %s: %w", in.ID, err)
	}

	if err := s.live.MarkRestoreApplying(ctx, sc.Fence, in.ID, pre.ID, out); err != nil {
		return nil, fmt.Errorf("mark restore %s applying: %w", in.ID, err)
	}

	return nil, nil
}

// startPreBackup starts the backup before restore in and runs it to its end
// on this step. A backup with that ID
// that exists already counts only when the input of its operation names this
// restore: an earlier try of this step, before a takeover. Any other backup,
// for example one that another restore or a person started, stops the
// restore with ErrPreBackupForeign and its audit event: the restore must not
// take the findings of a backup that did not read the live data for it.
func (s *RestoreService) startPreBackup(ctx context.Context, fence durable.StepFence, in restoreInput) error {
	err := s.backups.RunBeforeRestore(ctx, in.PreBackupID, in.ID)
	if err == nil {
		return nil
	}

	if !errors.Is(err, ErrAlreadyExists) {
		return fmt.Errorf("start the backup of the live data before restore %s: %w", in.ID, err)
	}

	owner, err := s.backups.StartedForRestore(ctx, in.PreBackupID)
	if err != nil {
		return fmt.Errorf("read the backup of the live data before restore %s: %w", in.ID, err)
	}

	if owner != in.ID {
		return s.failWithEvent(ctx, in, fence, preBackupForeignAuditObject(in),
			fmt.Errorf("backup %s: %w", in.PreBackupID, ErrPreBackupForeign))
	}

	return nil
}

// preBackupResult is the result of the pre-backup step, stored in the step
// record under the fence of the step: what the backup before the restore
// found at the live blobs.
type preBackupResult struct {
	Found BlobFindings `json:"found"`
}

// preBackupFindingsOf returns what the backup before the restore of op found,
// from the stored result of the pre-backup step. It reports false for a step
// with no result, or with a result that does not decode.
func preBackupFindingsOf(op durable.Operation) (BlobFindings, bool) {
	none := BlobFindings{Named: 0, Missing: 0, Damaged: 0, Keys: nil}

	rec, ok := op.Step(RestoreStepPreBackup)
	if !ok || len(rec.Result) == 0 {
		return none, false
	}

	var res preBackupResult
	if err := json.Unmarshal(rec.Result, &res); err != nil {
		return none, false
	}

	return res.Found, true
}

// checkPreBackupResult refuses the copy of blobs when the operation of sc
// holds no stored result of its pre-backup step, or when the result names
// damaged live blobs or more missing live blobs than the bound allows.
func (s *RestoreService) checkPreBackupResult(ctx context.Context, sc durable.StepContext, in restoreInput) error {
	found, ok := preBackupFindingsOf(sc.Operation)
	if !ok {
		return s.failWithEvent(ctx, in, sc.Fence, preBackupForeignAuditObject(in),
			fmt.Errorf("no result of the pre-backup step: %w", ErrPreBackupForeign))
	}

	if refused := checkFindings(found, EffectiveMissingBlobLimit(), found.Keys, found.Keys); refused != nil {
		return s.failWithEvent(ctx, in, sc.Fence, copyRefusedAuditObject(in, found), refused)
	}

	return nil
}

// recordPreBackupFindings stores one audit event of restore in when the
// backup pre before it found live blobs missing or damaged. The event is
// stored before the step is recorded and before the check of the bound, and
// a second store of one event changes nothing.
func (s *RestoreService) recordPreBackupFindings(
	ctx context.Context, fence durable.StepFence, in restoreInput, pre ID, found BlobFindings,
) error {
	if found.Empty() {
		return nil
	}

	if err := s.live.RecordRestoreEvent(ctx, fence, in.ID, preBackupFindingsAuditObject(in, pre, found)); err != nil {
		return fmt.Errorf("record the missing and damaged live blobs of restore %s: %w", in.ID, err)
	}

	s.logger.WarnContext(ctx, "backup before restore found live blobs that are missing or damaged",
		slog.String("restore_id", in.ID.String()), slog.String("backup_id", pre.String()),
		slog.Int("missing", found.Missing), slog.Int("damaged", found.Damaged), slog.Int("named", found.Named))

	return nil
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

	// Defense in depth: the pre-backup step stops the restore at damaged
	// live blobs and at missing live blobs above the bound, and it refuses a
	// backup that this restore did not start. This check stays so that a
	// pre-backup step that was skipped, or a result that is gone, cannot let
	// the first write of a blob happen. It reads only the step record of
	// this operation, not the backup target or the backups table.
	if refused := s.checkPreBackupResult(ctx, sc, in); refused != nil {
		return nil, refused
	}

	manifest, err := s.readPinnedManifest(ctx, in.BackupID, st.pinned())
	if err != nil {
		return nil, err
	}

	// The bound stops a new backup. A manifest above it stays restorable: an
	// earlier version of the server wrote it, and the restore records the
	// count and the bound in the audit log before it copies the first blob.
	limit := s.unverifiedLimit()

	if count := len(manifest.Unverified); count > limit {
		if recErr := s.recordUnverifiedOverLimit(ctx, sc.Fence, in, count, limit); recErr != nil {
			return nil, recErr
		}
	}

	return nil, s.copyManifestBlobs(ctx, sc.Fence, in, manifest)
}

// copyManifestBlobs copies each blob of the manifest into the live store.
//
// It checks the keys of the whole manifest before the first live key is
// written, so a refused key leaves no blob that an earlier entry wrote.
// Defense in depth: the stage, the check and the digest of the pinned manifest
// each stop a swapped manifest too. This check stays so that the copy does not
// depend on them.
func (s *RestoreService) copyManifestBlobs(
	ctx context.Context, fence durable.StepFence, in restoreInput, manifest Manifest,
) error {
	if keyErr := s.refuseKeys(ctx, fence, in, manifest.Blobs); keyErr != nil {
		return keyErr
	}

	if copyErr := s.copyBlobs(ctx, in, fence, manifest.Blobs); copyErr != nil {
		return fmt.Errorf("copy blobs for restore %s: %w", in.ID, copyErr)
	}

	return nil
}

// copyBlobs copies each blob of refs with copyBlob, at most
// restoreCopyConcurrency at a time. Each copy runs its own checks, and
// each write to the live store has its own fence check: no check covers
// more than one blob. The first error cancels the copies that run and stops
// the copies that wait.
//
// A key that refs names twice is copied once, so two copies never use one
// temporary key. The entries must agree: a key with two different sizes,
// checksums or seal flags fails the restore before any copy, as ErrCorrupt.
func (s *RestoreService) copyBlobs(
	ctx context.Context, in restoreInput, fence durable.StepFence, refs []ManifestBlob,
) error {
	unique, err := s.uniqueBlobs(ctx, fence, in, refs)
	if err != nil {
		return err
	}

	return runBounded(ctx, unique, restoreCopyConcurrency, func(ctx context.Context, ref ManifestBlob) error {
		return s.copyBlob(ctx, in, fence, ref)
	})
}

// runBounded runs do for each item of items, at most limit at a time. The
// first error cancels the context of the runs that go on, stops the items that
// wait, and is the result. It returns only after every run ended.
func runBounded[T any](ctx context.Context, items []T, limit int, do func(context.Context, T) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		wg      sync.WaitGroup
		once    sync.Once
		first   error
		permits = make(chan struct{}, limit)
	)

	for _, item := range items {
		select {
		case permits <- struct{}{}:
		case <-ctx.Done():
		}

		if ctx.Err() != nil {
			break
		}

		wg.Go(func() {
			defer func() { <-permits }()

			if err := do(ctx, item); err != nil {
				once.Do(func() {
					first = err

					cancel()
				})
			}
		})
	}

	wg.Wait()

	if first != nil {
		return first
	}

	if err := ctx.Err(); err != nil {
		return fmt.Errorf("copy blobs: %w", err)
	}

	return nil
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

	entries, err := s.afterApplyEntries(ctx, sc.Operation, in, swappedFinished)
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

	return s.checkNamedBlobs(ctx, in, sc.Fence, manifest.Blobs)
}

// afterApplyEntries returns the audit entries of the restore of op: its own
// entry, the entries of its audit events (the work that its enter canceled
// and the live blobs that it refused to overwrite), which the swap removed with the old
// audit log, and the entries of the dev data report.
func (s *RestoreService) afterApplyEntries(
	ctx context.Context, op durable.Operation, in restoreInput, end swappedEnd,
) ([]audit.Entry, error) {
	st, _, err := stageResultOf(op)
	if err != nil {
		return nil, err
	}

	events, err := s.live.RestoreEvents(ctx, in.ID)
	if err != nil {
		return nil, fmt.Errorf("read the audit events of restore %s: %w", in.ID, err)
	}

	own := s.restoreAuditEntry(in, s.restoreAuditObject(in, end))
	if end == swappedFailed {
		own.Outcome = audit.OutcomeFailure
	}

	entries := []audit.Entry{own}

	for _, object := range events {
		entries = append(entries, s.restoreAuditEntry(in, object))
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

	report = withPreBackupFindings(report, sc.Operation)

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

// withPreBackupFindings returns report with what the backup before the
// restore of op found at the live blobs, and the warning of missing live
// blobs when it found any. A restore that went on found no damaged blob.
func withPreBackupFindings(report DevStripReport, op durable.Operation) DevStripReport {
	found, ok := preBackupFindingsOf(op)
	if !ok || found.Empty() {
		return report
	}

	report.PreBackupFound = &found

	if found.Missing > 0 {
		report.Warnings = append(slices.Clone(report.Warnings), WarnMissingLiveBlobs)
	}

	return report
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
// reports durable.ErrClaimLost unless the claim of the fence still holds it.
// Then it stores the cancel of unit as an audit event of the restore, under
// the fence, so the record exists before the cancel and survives the swap and
// a takeover, and writes the log line of the attempt. Every attempt gets this
// line, also one that cancels nothing.
func (w restoreWriter) Check(ctx context.Context, unit string) error {
	op, err := w.s.operations.Get(ctx, w.fence.Operation)
	if err != nil {
		return fmt.Errorf("read the operation of restore %s: %w", w.id, err)
	}

	// Defense in depth: the fenced write below checks the claim in its
	// transaction too; this read stays so that a lost claim stops before any
	// write.
	if _, done := op.Step(w.fence.Step); op.State != durable.StateRunning || op.Claim.Token != w.fence.Token ||
		op.CancelRequested || done {
		return fmt.Errorf("restore %s: %w", w.id, durable.ErrClaimLost)
	}

	object := canceledAuditObject(w.id, w.fence.Token, unit)
	if err := w.s.live.RecordRestoreEvent(ctx, w.fence, w.id, object); err != nil {
		return fmt.Errorf("record the cancel of %s for restore %s: %w", unit, w.id, err)
	}

	w.s.logger.WarnContext(ctx, "restore attempted to cancel the active work", slog.String("restore_id", w.id.String()),
		slog.Int64("token", w.fence.Token), slog.String("unit", unit))

	return nil
}

// Actor implements maintenance.StateWriter.
func (w restoreWriter) Actor() string {
	return "restore " + w.id.String() + " claim token " + strconv.FormatInt(w.fence.Token, 10)
}

// logCanceled writes one log line for each unit of work that an enter of the
// restore canceled and confirmed, with the restore ID, the claim token and
// the unit name. The writer writes the line of every attempt, before the
// cancel, so this line names only the confirmed cancels. The audit entry of
// each cancel comes from the audit event that the writer stored before the
// cancel.
func (s *RestoreService) logCanceled(ctx context.Context, in restoreInput, fence durable.StepFence, units []string) {
	if len(units) == 0 {
		return
	}

	for _, unit := range units {
		s.logger.WarnContext(ctx, "restore canceled the active work", slog.String("restore_id", in.ID.String()),
			slog.Int64("token", fence.Token), slog.String("unit", unit))
	}
}

// differingBlobAuditObject names the refusal to overwrite the live key by
// restore in, for its audit entry: the live key holds other bytes than the
// backup, and the restore stops with ErrCorrupt and writes nothing over them.
func differingBlobAuditObject(in restoreInput, key string) string {
	return "restore " + in.ID.String() + " refused to overwrite live blob " + strconv.Quote(truncateKey(key)) +
		" whose bytes differ from backup " + in.BackupID.String()
}

// preBackupFindingsAuditObject names what the backup pre before restore in
// found at the live blobs, for its audit entry: the counts and the first
// keys. The keys come from checked entries of the manifest, quoted.
func preBackupFindingsAuditObject(in restoreInput, pre ID, found BlobFindings) string {
	quoted := make([]string, 0, len(found.Keys))
	for _, key := range found.Keys {
		quoted = append(quoted, strconv.Quote(key))
	}

	return "backup " + pre.String() + " before restore " + in.ID.String() + " found " +
		strconv.Itoa(found.Missing) + " missing and " + strconv.Itoa(found.Damaged) + " damaged live blobs of " +
		strconv.Itoa(found.Named) + ", first " + strings.Join(quoted, ", ")
}

// copyRefusedAuditObject names the stop of restore in at the copy step, for
// its audit entry: the stored result of the backup before it names damaged
// live blobs, or more missing live blobs than the bound allows.
func copyRefusedAuditObject(in restoreInput, found BlobFindings) string {
	return "restore " + in.ID.String() + " refused to copy blobs: the backup before it found " +
		strconv.Itoa(found.Missing) + " missing and " + strconv.Itoa(found.Damaged) + " damaged live blobs of " +
		strconv.Itoa(found.Named)
}

// preBackupForeignAuditObject names the stop of restore in at a backup before
// it that it did not start, for its audit entry.
func preBackupForeignAuditObject(in restoreInput) string {
	return "restore " + in.ID.String() + " refused backup " + in.PreBackupID.String() +
		" as the backup before it: this restore did not start it"
}

// stagedDiffersAuditObject names the stop of restore in at a blob whose staged
// copy differs from the backup, for its audit entry. The restore wrote
// nothing to the live key.
func stagedDiffersAuditObject(in restoreInput, key string) string {
	return "restore " + in.ID.String() + " stopped at blob " + strconv.Quote(truncateKey(key)) +
		": the staged copy differs from backup " + in.BackupID.String() + ", nothing was written to the live key"
}

// writtenDiffersAuditObject names the stop of restore in at a blob whose bytes
// failed the check after the write to the live key, for its audit entry. The
// live key holds the bytes that failed the check.
func writtenDiffersAuditObject(in restoreInput, key string) string {
	return "restore " + in.ID.String() + " wrote blob " + strconv.Quote(truncateKey(key)) +
		" and its bytes failed the check after the write, backup " + in.BackupID.String()
}

// canceledAuditObject names the attempt to cancel one unit of active work by
// the enter of restore id with the claim token, for its audit entry. The
// writer stores the entry before the cancel runs, so the text names an
// attempt, not a result.
func canceledAuditObject(id RestoreID, token int64, unit string) string {
	return "restore " + id.String() + " claim token " + strconv.FormatInt(token, 10) +
		" attempted to cancel the active work: " + unit
}

// endBeforeSwapAuditObject names the end of restore in before its swap, for
// its audit entry. The swap did not run, so the live state, the history and
// the access tables did not change. The text names no table as replaced.
func endBeforeSwapAuditObject(in restoreInput, end string) string {
	return "restore " + in.ID.String() + " of backup " + in.BackupID.String() + " " + end +
		" before the swap. The live state, history and access tables did not change."
}

// restoreAuditObject names one restore that swapped for its audit entry, with
// whether the access tables were replaced or kept, and, when end is swappedFailed,
// that the restore failed after the swap.
func (s *RestoreService) restoreAuditObject(in restoreInput, end swappedEnd) string {
	mode := "replaced"
	if in.KeepAccessTables {
		mode = "kept"
	}

	verb := ""
	if end == swappedFailed {
		verb = " failed after the swap"
	}

	return "restore " + in.ID.String() + " of backup " + in.BackupID.String() + verb + " (access tables " + mode +
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

// copyBlob refuses a key that a restore does not write, then copies the blob
// of ref into the live store with copyCheckedKey.
//
// Defense in depth: the steps check the whole manifest before they copy. This
// check stays so that no caller of copyBlob, such as the repair of
// after-apply, can write a key that a restore does not write.
func (s *RestoreService) copyBlob(
	ctx context.Context,
	in restoreInput,
	fence durable.StepFence,
	ref ManifestBlob,
) error {
	if keyErr := s.refuseKeys(ctx, fence, in, []ManifestBlob{ref}); keyErr != nil {
		return keyErr
	}

	return s.copyCheckedKey(ctx, in, fence, ref)
}

// copyCheckedKey copies one blob of the backup into the live store with
// checked bytes. Blobs are write-once: it never replaces a live key that it
// saw, and it never deletes one:
//
//  1. A live key that holds the bytes of the manifest stays.
//  2. A live key with other bytes stops the restore with ErrCorrupt. The audit
//     event of the refusal is stored under the fence, and nothing is written.
//  3. For an absent live key, the bytes of the backup go to a temporary key
//     of this restore and token, with the check of the size and the checksum.
//     The staged bytes are read and checked again before any write to the live
//     key. Bytes that differ stop the restore with ErrCorrupt, delete only the
//     temporary key, and write nothing to the live key.
//  4. The claim is checked, then the live key is read again, then the checked
//     bytes go to the live key. A store that refuses the write because the key
//     exists is not trusted: the live key is read once more, and it must hold
//     the bytes of the manifest.
//
// The restore does not rely on a conditional write of the store, but one gap
// stays on a store that ignores If-None-Match: a writer that creates the live
// key between the second read in step 4 and the write is not seen, and the
// restore replaces its bytes. Maintenance mode stops every other writer
// before the copy steps run, and the fence stops a holder that lost its claim.
// The claim can also expire between its check and the write. The lease bounds
// that gap, and the next holder writes the same checked bytes.
func (s *RestoreService) copyCheckedKey(
	ctx context.Context,
	in restoreInput,
	fence durable.StepFence,
	ref ManifestBlob,
) error {
	absent, err := s.liveKeyIsAbsent(ctx, in, fence, ref)
	if err != nil || !absent {
		return err
	}

	tmp, err := s.stageTemp(ctx, in.ID, fence.Token, ref)
	if err != nil {
		return err
	}

	// A failure from here on removes the temporary key. A failed delete leaves
	// it to the cleanup step.
	err = s.writeStaged(ctx, in, fence, ref, tmp)
	if delErr := s.blobs.Delete(ctx, tmp); delErr != nil {
		s.logger.WarnContext(ctx, "temporary restore blob was not deleted", slog.String("restore_id", in.ID.String()))
	}

	return err
}

// writeStaged checks the staged bytes at tmp, checks the claim, reads the live
// key again, and writes the staged bytes to the live key.
func (s *RestoreService) writeStaged(
	ctx context.Context, in restoreInput, fence durable.StepFence, ref ManifestBlob, tmp blob.Key,
) error {
	live, err := blob.ParseKey(ref.Key)
	if err != nil {
		return fmt.Errorf("restore blob key %q: %w", ref.Key, err)
	}

	// The check comes before the write, so that the restore never writes bytes
	// that it knows to be bad. A read error is not corruption: the step retries.
	if stagedErr := s.verifyObject(ctx, tmp, ref, "staged copy of blob"); stagedErr != nil {
		if !errors.Is(stagedErr, ErrCorrupt) {
			return stagedErr
		}

		return s.failWithEvent(ctx, in, fence, stagedDiffersAuditObject(in, ref.Key), stagedErr)
	}

	// The write changes a store that the fence does not guard, so the claim
	// is checked just before it. A holder that lost its claim writes nothing.
	if fenceErr := s.live.CheckRestoreFence(ctx, fence, in.ID); fenceErr != nil {
		return fmt.Errorf("check the claim before the write of blob %q: %w", ref.Key, fenceErr)
	}

	// Defense in depth: a store that ignores If-None-Match does not refuse a
	// write to a key that exists. The key is read again here, after the copy
	// and the claim check, so that the gap to the write is one read and one
	// write.
	absent, err := s.liveKeyIsAbsent(ctx, in, fence, ref)
	if err != nil || !absent {
		return err
	}

	putErr := s.putChecked(ctx, copyFrom{store: s.blobs, key: tmp, target: false}, live, ref)

	switch {
	case putErr == nil:
		return nil
	case errors.Is(putErr, blob.ErrExists):
		return s.settleExisting(ctx, in, fence, ref)
	case errors.Is(putErr, ErrCorrupt):
		return s.failWithEvent(ctx, in, fence, writtenDiffersAuditObject(in, ref.Key), putErr)
	default:
		return putErr
	}
}

// liveKeyIsAbsent reads the live key of ref and reports whether the copy goes
// on. It reports true when the key is absent. It reports false with no error
// when the key holds the bytes of the manifest, so there is nothing to write.
// Other bytes stop the restore as corrupt, with the audit event of the
// refusal: a restore never writes over a live blob. Any other error, for
// example a throttled store, returns as it is.
func (s *RestoreService) liveKeyIsAbsent(
	ctx context.Context, in restoreInput, fence durable.StepFence, ref ManifestBlob,
) (bool, error) {
	verifyErr := s.verifyLiveBlob(ctx, ref)

	switch {
	case verifyErr == nil:
		return false, nil
	case isNotFound(verifyErr):
		return true, nil
	case errors.Is(verifyErr, ErrCorrupt):
		return false, s.refuseDifferingBlob(ctx, in, fence, ref, verifyErr)
	default:
		return false, verifyErr
	}
}

// settleExisting resolves a write that the store refused because the live key
// exists. The key must hold the bytes of the manifest. Only a read that gives
// ErrCorrupt stops the restore as corrupt: any other error, for example a
// throttled store, returns as it is, so that the step retries and no event
// says that bytes differ.
func (s *RestoreService) settleExisting(
	ctx context.Context, in restoreInput, fence durable.StepFence, ref ManifestBlob,
) error {
	verifyErr := s.verifyLiveBlob(ctx, ref)

	switch {
	case verifyErr == nil:
		return nil
	case errors.Is(verifyErr, ErrCorrupt):
		return s.refuseDifferingBlob(ctx, in, fence, ref, verifyErr)
	default:
		return verifyErr
	}
}

// refuseDifferingBlob stops the restore at a live key whose bytes differ from
// the manifest. See failWithEvent.
func (s *RestoreService) refuseDifferingBlob(
	ctx context.Context, in restoreInput, fence durable.StepFence, ref ManifestBlob, cause error,
) error {
	refusal := fmt.Errorf("live blob %q differs from backup %s and the restore does not overwrite it: %w",
		truncateKey(ref.Key), in.BackupID, cause)

	return s.failWithEvent(ctx, in, fence, differingBlobAuditObject(in, ref.Key), refusal)
}

// failWithEvent stores object as an audit event of the restore under the fence,
// and returns cause, which wraps ErrCorrupt. It fails closed: when the event
// cannot be stored, the restore is refused all the same, and the error of the
// store is joined. A lost claim or a cancel stops the step instead.
func (s *RestoreService) failWithEvent(
	ctx context.Context, in restoreInput, fence durable.StepFence, object string, cause error,
) error {
	recordErr := s.live.RecordRestoreEvent(ctx, fence, in.ID, object)
	if recordErr == nil {
		return cause
	}

	if errors.Is(recordErr, durable.ErrClaimLost) || errors.Is(recordErr, durable.ErrCanceled) {
		return recordErr
	}

	return errors.Join(cause, fmt.Errorf("record the audit event of restore %s: %w", in.ID, recordErr))
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

// checkNamedBlobs checks that the live store holds each blob that the
// restored data names, and copies a missing one again from the backup. A
// deleter that stalled across the restore can have deleted one.
func (s *RestoreService) checkNamedBlobs(
	ctx context.Context, in restoreInput, fence durable.StepFence, refs []ManifestBlob,
) error {
	if err := s.copyBlobs(ctx, in, fence, refs); err != nil {
		return fmt.Errorf("check the blobs of restore %s: %w", in.ID, err)
	}

	return nil
}

// deleteTempKeys deletes the temporary blob keys of restore id and of each
// restore whose row ended. The keys of a restore with no row stay: another
// installation can share the store.
func (s *RestoreService) deleteTempKeys(ctx context.Context, id RestoreID) error {
	objects, err := listObjects(ctx, s.blobs, tempKeyPrefix)
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

// manifestBlobs converts the blob references of a summary to manifest entries.
func manifestBlobs(refs []BlobRef) []ManifestBlob {
	out := make([]ManifestBlob, 0, len(refs))

	for _, ref := range refs {
		out = append(out, ManifestBlob{Key: ref.Key, Size: ref.Size, MD5: "", Sealed: ref.Sealed})
	}

	return out
}

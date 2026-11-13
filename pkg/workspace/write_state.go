package workspace

import (
	"context"
	"crypto/md5" //nolint:gosec // The TFE protocol and the http backend name state by MD5 for integrity, not security.
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/internal/token"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// refuseTimeout bounds the cleanup of a refused streamed write.
const refuseTimeout = 30 * time.Second

// placeholderSerial marks the serial of a streamed version before its body
// is read. Readers never see it: a placeholder is pending until it finalizes.
const placeholderSerial int64 = -1

// StreamInput is one state written as a single streamed body (ADR 49, decision
// 7). The serial and the lineage come from the body itself, so the write
// declares no metadata before it streams.
type StreamInput struct {
	// Holder is the principal the write runs as. It holds the lock named by
	// LockID, or it is the principal that makes an unlocked write.
	Holder string
	// LockID is the lock ID the client names with ?ID=. It is empty when the
	// client names none: the write then needs an unlocked unit and the
	// unlocked-write override (ADR 44, decision 5).
	LockID string
	// MD5 is the hex MD5 the client declared with Content-MD5. It is empty when
	// the client declared none: the MD5 the bytes produce is recorded instead.
	MD5 string
}

// StreamAuthority is the caller's side of a streamed write: the overrides it may
// use, and the callback that writes the audit entry of each override the write
// uses. Record receives the serial of the body, which is known only after the
// body streams.
type StreamAuthority struct {
	// Allow holds the overrides the caller may use.
	Allow namespace.Overrides
	// Record writes the audit entry of one override with the serial of the body.
	// A write that uses an override while Record is nil is refused with
	// ErrNoRecorder before it writes. A failed record refuses the write.
	Record func(Override, int64) error
}

// WriteState stores one state version streamed from body, for the caller
// holder. It checks the lock of in and the overrides of auth before the body
// streams. After the body streams and before the finalize, it records each
// override that the write uses, with the serial of the body (ADR 41, decision 5). It records the attempt
// before the first byte, so a sweep finds the blob even when the write stops
// midway. After the body streams, it finalizes the version in one store
// transaction. That transaction runs the namespace fence and the lock checks,
// and the serial and lineage checks of ADR 25. A refused version deletes its own
// blob. The returned version carries the plain outputs of the body, and none for
// a state the client encrypted.
//
// An error from the body or from the blob store leaves the attempt for the
// sweep. The outcome is unknown when a finalize fails for another reason than
// a refusal, so its blob stays.
func (s *Service) WriteState(
	ctx context.Context, id ID, in StreamInput, auth StreamAuthority, body io.Reader,
) (StateVersion, error) {
	sum, hasSum, err := parseStreamMD5(in.MD5)
	if err != nil {
		return StateVersion{}, err
	}

	w, err := s.GetByID(ctx, id)
	if err != nil {
		return StateVersion{}, err
	}

	sw, err := s.admitStream(ctx, w, in, auth)
	if err != nil {
		return StateVersion{}, err
	}

	sw.want = sum
	sw.checked = hasSum

	return s.streamVersion(ctx, sw, body)
}

// streamWrite is one admitted streamed version: its placeholder row, the blob
// key of its attempt, the fence it runs under, and the MD5 the body must match
// when the client declared one.
type streamWrite struct {
	workspace Workspace
	version   StateVersion
	key       blob.Key
	fence     namespace.Fence
	want      [md5.Size]byte
	checked   bool
	// used holds the overrides the write uses. record writes their entries after
	// the body streams, once its serial is known.
	used   []Override
	record func(Override, int64) error
}

// streamAdmission is what the checks before a streamed write find: whether the
// write passes the lock as an override, the overrides it uses, and its fence.
type streamAdmission struct {
	unlocked bool
	used     []Override
	fence    namespace.Fence
}

// noAdmission is the streamAdmission that comes with a refusal.
func noAdmission() streamAdmission {
	return streamAdmission{unlocked: false, used: nil, fence: namespace.Fence{Namespace: "", FailoverVersion: 0}}
}

// checkStreamAdmission runs the checks that come before a streamed write
// declares its placeholder row, and writes nothing. It checks the lock, the
// overrides, the manual lock and the fence. It returns whether the write passes
// the lock as an override, the overrides it uses, and the fence of the write.
func (s *Service) checkStreamAdmission(
	ctx context.Context, w Workspace, in StreamInput, auth StreamAuthority,
) (streamAdmission, error) {
	if lockErr := checkStreamLock(w, in.LockID); lockErr != nil {
		return noAdmission(), lockErr
	}

	unlocked, used, err := declareOverrides(
		w,
		in.Holder,
		NewState{Lineage: "", MD5: "", Outputs: nil, Serial: 0, Force: false},
		nil,
		auth.Allow,
	)
	if err != nil {
		return noAdmission(), fmt.Errorf("new state for %s: %w", w.Ref, err)
	}

	if auth.Record == nil {
		if recErr := refuseWithoutRecorder(w, used); recErr != nil {
			return noAdmission(), recErr
		}
	}

	// A write without a lock ID passes the workspace lock when the caller may
	// override it. A manual lock stops it all the same: the lock covers every
	// change to the workspace.
	if unlocked {
		if lockErr := s.refuseManualLock(ctx, w, ""); lockErr != nil {
			return noAdmission(), fmt.Errorf("write state of %s: %w", w.Ref, lockErr)
		}
	}

	fence, err := s.fence(ctx, w.Ref.Namespace)
	if err != nil {
		return noAdmission(), err
	}

	return streamAdmission{unlocked: unlocked, used: used, fence: fence}, nil
}

// AdmitFirstWrite reports whether a streamed write to the workspace called
// ref would pass the checks that come before the body streams, when the
// workspace does not exist yet. It returns the error that WriteState would
// return for the new workspace, and nil when the write could go on. It writes
// nothing, so a caller creates the workspace only after it returns nil: a
// refused first request leaves no workspace.
//
// A new workspace is unlocked and has both override switches off, so a write
// to it needs the lock that no one holds, or an override that is off: the
// check refuses every first write today. The check stays a call to the same
// code that WriteState runs, so the two cannot disagree.
func (s *Service) AdmitFirstWrite(ctx context.Context, ref Ref, in StreamInput) error {
	if _, err := s.writable(ctx, ref.Namespace); err != nil {
		return err
	}

	_, err := s.checkStreamAdmission(ctx, newWorkspaceOf(ref), in, StreamAuthority{
		Allow:  namespace.Overrides{Forced: false, Unlocked: false},
		Record: nil,
	})

	return err
}

// admitStream checks the lock and the overrides of a streamed write, records
// each override it uses, and declares its placeholder row. The attempt is noted
// before the body streams, so a write that stops midway leaves a row that names
// its blob.
func (s *Service) admitStream(
	ctx context.Context, w Workspace, in StreamInput, auth StreamAuthority,
) (*streamWrite, error) {
	adm, err := s.checkStreamAdmission(ctx, w, in, auth)
	if err != nil {
		return nil, err
	}

	unlocked, used, fence := adm.unlocked, adm.used, adm.fence

	sv, key, err := s.declarePlaceholder(ctx, w, fence, unlocked)
	if err != nil {
		return nil, err
	}

	if noteErr := s.noteAttempt(ctx, w, sv.ID, key, fence); noteErr != nil {
		return nil, noteErr
	}

	return &streamWrite{
		workspace: w,
		version:   sv,
		key:       key,
		fence:     fence,
		want:      [md5.Size]byte{},
		checked:   false,
		used:      used,
		record:    auth.Record,
	}, nil
}

// streamVersion streams the body into the blob of sw, reads its serial, lineage
// and outputs as it passes, and finalizes the version. A refusal with a known
// outcome ends the placeholder with refuseStream. A failed store write is refused
// the same way: the body did not land, so nothing can name its blob.
func (s *Service) streamVersion(ctx context.Context, sw *streamWrite, body io.Reader) (StateVersion, error) {
	sv := sw.version

	grow := func(n int) error {
		return charge(ctx, int64(n))
	}

	reader, stream := newMetaStream(body, grow)

	stored, storeErr := s.storeContent(ctx, storeInput{
		namespace:   sw.workspace.Ref.Namespace,
		namespaceID: sw.workspace.NamespaceID,
		workspace:   sw.workspace.ID,
		version:     string(sv.ID),
		key:         sw.key,
		want:        sw.want,
		content:     reader,
		unchecked:   !sw.checked,
	})

	meta, err := stream.finish(storeErr)
	if err != nil {
		return StateVersion{}, s.refuseStream(ctx, sw, fmt.Errorf("write state %s: %w", sv.ID, err))
	}

	if metaErr := checkStreamedMeta(meta); metaErr != nil {
		return StateVersion{}, s.refuseStream(ctx, sw, fmt.Errorf("write state %s: %w", sv.ID, metaErr))
	}

	outputs, err := s.streamedOutputs(ctx, sw.workspace, string(sv.ID), meta)
	if err != nil {
		return StateVersion{}, s.refuseStream(ctx, sw, err)
	}

	if recErr := sw.recordUsed(meta.Serial); recErr != nil {
		return StateVersion{}, s.refuseStream(ctx, sw, recErr)
	}

	sv.Status = StatusFinalized
	sv.FinalizedAt = s.clock()
	sv.Content = sw.key
	sv.Size = stored.size
	sv.MD5 = stored.sum
	sv.Serial = meta.Serial
	sv.Lineage = meta.Lineage
	sv.Outputs = outputs.stored

	if finErr := s.finalizeStream(ctx, sw, sv); finErr != nil {
		return StateVersion{}, finErr
	}

	sv.Outputs = outputs.plain

	return sv, nil
}

// finalizeStream finalizes sv in one store transaction. The transaction runs
// the namespace fence, the lock checks and the serial and lineage checks of ADR
// 25. A rejected finalize wrote nothing, so the write is refused. An unknown
// outcome keeps the placeholder, because the version may point at its blob.
func (s *Service) finalizeStream(ctx context.Context, sw *streamWrite, sv StateVersion) error {
	finErr := s.states.FinalizeStateVersion(ctx, sv, sw.fence)
	if finErr == nil {
		return nil
	}

	wrapped := fmt.Errorf("finalize state %s: %w", sv.ID, finErr)
	if rejected(wrapped) {
		return s.refuseStream(ctx, sw, wrapped)
	}

	return wrapped
}

// refuseStream ends a streamed write whose outcome is known to be a refusal: no
// version became current. It deletes the blob first. It discards the placeholder
// row only once the blob is gone, so a failed blob delete leaves the row, and the
// sweep finds the blob through it. A refused placeholder left pending would fill
// the pending cap for the lock holder (ADR 49, decision 7).
func (s *Service) refuseStream(ctx context.Context, sw *streamWrite, cause error) error {
	cleanCtx, cancel := cleanupContext(ctx)
	defer cancel()

	if delErr := s.blobs.deleteRetried(cleanCtx, sw.workspace.NamespaceID, sw.workspace.ID, sw.key); delErr != nil {
		return errors.Join(cause, fmt.Errorf("delete refused blob of %s: %w", sw.version.ID, delErr))
	}

	if discardErr := s.states.DiscardStateVersion(cleanCtx, sw.version.ID, sw.fence); discardErr != nil {
		return errors.Join(cause, fmt.Errorf("discard refused state version %s: %w", sw.version.ID, discardErr))
	}

	return cause
}

// checkStreamLock refuses a streamed write whose lock ID does not name the
// current lock. A write without a lock ID needs an unlocked unit, so a locked
// unit refuses it with ErrLocked (ADR 49, decision 6).
func checkStreamLock(w Workspace, lockID string) error {
	switch {
	case lockID != "" && lockID != w.Lock.ID:
		return lockRefused(w.Lock, fmt.Errorf("%w: write names lock %q, unit %s", ErrLockMismatch, lockID, w.Ref))
	case lockID == "" && w.Lock.Locked():
		return lockRefused(w.Lock, fmt.Errorf("%w: write names no lock ID, unit %s", ErrLocked, w.Ref))
	default:
		return nil
	}
}

// declarePlaceholder admits one streamed declare and creates its placeholder
// row. The row holds no upload capability and no content: it is pending until
// the body finalizes it, and readers never see it. It returns the row and the
// blob key of its one attempt.
func (s *Service) declarePlaceholder(
	ctx context.Context, w Workspace, fence namespace.Fence, unlocked bool,
) (StateVersion, blob.Key, error) {
	unlock, err := s.admitDeclare(ctx, w)
	if err != nil {
		return StateVersion{}, "", err
	}

	defer unlock()

	now := s.clock()

	sv := StateVersion{
		CreatedAt:       now,
		FinalizedAt:     time.Time{},
		UploadExpiresAt: now.Add(UploadWindow),
		Upload:          token.Digest{},
		ID:              StateVersionID(ident.NewID("sv-")),
		Workspace:       w.ID,
		Holder:          w.Lock.Holder,
		LockID:          w.Lock.ID,
		Lineage:         "",
		Status:          StatusPending,
		Content:         "",
		Outputs:         nil,
		MD5:             [md5.Size]byte{},
		Serial:          placeholderSerial,
		Size:            0,
		Force:           false,
		UnlockedWrite:   unlocked,
		Sealed:          true,
		RestoredFrom:    "",
		SensitiveMarks:  nil,
		FenceBy:         "",
		Origin:          originFrom(ctx),
	}

	if createErr := s.states.CreateStateVersion(ctx, sv, fence, s.maxPending); createErr != nil {
		return StateVersion{}, "", fmt.Errorf("create state version for %s: %w", w.Ref, createErr)
	}

	key, err := s.blobs.attemptKey(w.NamespaceID, w.ID, sv.ID)
	if err != nil {
		return StateVersion{}, "", err
	}

	return sv, key, nil
}

// admitDeclare takes the declare stripe of w, sweeps its expired pending
// versions, and refuses with ErrTooManyPending when the workspace holds the
// maximum. It returns the function that releases the stripe. A cleanup failure
// never fails the declare, as in BeginState.
//
// This check is an early refusal and a defense-in-depth layer. It does not
// hold the cap alone: the stripe lives in this process, and the count here is
// read before the insert, so two instances on one database can both pass it.
// The cap holds in the store, which counts the pending versions and inserts
// the new one in one fenced transaction and answers ErrTooManyPending. The
// stripe stays to save the store from conflicting declares of one workspace in
// this process. Do not remove the check here: it spares a refused declare the
// work between this point and the insert.
func (s *Service) admitDeclare(ctx context.Context, w Workspace) (func(), error) {
	unlock := s.lockKey("begin/" + w.ID.String())

	if sweepErr := s.sweepExpired(ctx, w); sweepErr != nil {
		s.logger.WarnContext(
			ctx,
			"sweep expired state",
			slog.String("workspace", w.ID.String()),
			slog.String("error", sweepErr.Error()),
		)
	}

	pending, err := s.states.CountPendingStateVersions(ctx, w.ID)
	if err != nil {
		unlock()

		return nil, fmt.Errorf("count pending state of %s: %w", w.Ref, err)
	}

	if pending >= s.maxPending {
		unlock()

		return nil, fmt.Errorf("%w for %s: %d pending, limit %d", ErrTooManyPending, w.Ref, pending, s.maxPending)
	}

	return unlock, nil
}

// sealedOutputsCharge is how many times the plain outputs size a seal asks of
// the budget: the sealed copy, and the buffers that sealing allocates.
const sealedOutputsCharge = 2

// streamedOutputs is the outputs of a streamed version: stored holds what the
// row keeps, plain holds what the caller reads.
type streamedOutputs struct {
	stored []byte
	plain  []byte
}

// streamedOutputs returns the outputs to store and the plain outputs to return.
// A state the client encrypted has no readable outputs, so it stores none. A
// version stores the outputs sealed with the data key of the workspace
// scope.
func (s *Service) streamedOutputs(
	ctx context.Context, w Workspace, versionID string, meta streamedMeta,
) (streamedOutputs, error) {
	if meta.ClientEncrypted {
		return streamedOutputs{stored: nil, plain: nil}, nil
	}

	plain := meta.Outputs
	if len(plain) == 0 {
		return streamedOutputs{stored: plain, plain: plain}, nil
	}

	// The sealed copy is as large as the plain one, and sealing it allocates
	// buffers that the collector frees later. Charge twice the plain size.
	if chargeErr := charge(ctx, sealedOutputsCharge*int64(len(plain))); chargeErr != nil {
		return streamedOutputs{stored: nil, plain: nil}, chargeErr
	}

	sealedOutputs, err := s.sealer.SealOutputs(ctx, w.Ref.Namespace, w.ID.String(), versionID, plain)
	if err != nil {
		return streamedOutputs{stored: nil, plain: nil}, fmt.Errorf("seal outputs of %s: %w", w.Ref, err)
	}

	return streamedOutputs{stored: sealedOutputs, plain: plain}, nil
}

// checkStreamedMeta checks the serial and the lineage that the body declared.
// The finalize transaction checks them against the current state (ADR 25).
func checkStreamedMeta(meta streamedMeta) error {
	switch {
	case !meta.HasSerial:
		return fmt.Errorf("%w: state has no serial", ErrInvalidState)
	case !meta.HasLineage:
		return fmt.Errorf("%w: state has no lineage", ErrInvalidState)
	case meta.Serial < 0:
		return fmt.Errorf("%w: serial %d", ErrInvalidState, meta.Serial)
	case meta.Lineage == "", len(meta.Lineage) > maxLineageLength:
		return fmt.Errorf("%w: lineage %q", ErrInvalidState, meta.Lineage)
	default:
		return nil
	}
}

// parseStreamMD5 decodes the hex MD5 a streamed write declared. An empty value
// declares none, and the bool reports whether one was declared.
func parseStreamMD5(s string) ([md5.Size]byte, bool, error) {
	var sum [md5.Size]byte

	if s == "" {
		return sum, false, nil
	}

	raw, err := hex.DecodeString(s)
	if err != nil || len(raw) != md5.Size {
		return sum, false, fmt.Errorf("%w: md5 %q", ErrInvalidState, s)
	}

	copy(sum[:], raw)

	return sum, true, nil
}

// recordUsed writes the audit entry of each override the write uses, with the
// serial of the body. It stops at the first entry that fails, and the write
// does not finalize.
func (sw *streamWrite) recordUsed(serial int64) error {
	if sw.record == nil {
		return refuseWithoutRecorder(sw.workspace, sw.used)
	}

	for _, override := range sw.used {
		if err := sw.record(override, serial); err != nil {
			return fmt.Errorf("record override of %s: %w", sw.workspace.Ref, err)
		}
	}

	return nil
}

// cleanupContext returns the context of the cleanup of a refused write. The
// cleanup outlives the request: a client that disconnects cancels ctx, and the
// delete and the discard must still run. The timeout bounds them.
func cleanupContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), refuseTimeout)
}

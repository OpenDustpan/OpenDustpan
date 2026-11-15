package job

import (
	"context"
	"crypto/sha256"
	"fmt"
	"slices"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
)

// MaxLogChunks is the largest number of chunks in the log of one step. With
// MaxLogChunk it bounds a log to 64 GiB.
const MaxLogChunks = 1 << 16

// MaxExitCode is the largest exit code that EndStep accepts. A process that
// ended on a signal or did not start has the exit code -1.
const MaxExitCode = 255

// LogClose is what EndStep gives the store to close the log version of a
// step: the number of chunks, the hash of the plaintext of all chunks in
// order and its size. The recorder reads it back from the chunks that
// the store holds, so an executor cannot give another content hash than the
// one that the chunks hold.
type LogClose struct {
	Version string
	Chunks  int
	SHA256  string
	Size    int64
}

// NewItemKeyFunc creates the item key of a new plan or log version in the
// transaction tx of the store, under fence. secret.ItemSealer.NewItemKey has
// this form. The store calls it in the write transaction that stores the item,
// so the record of the key commits with the item row.
type NewItemKeyFunc func(
	ctx context.Context, tx secret.ItemKeyTx, ref secret.ItemRef, fence namespace.Fence,
) (secret.ItemKey, error)

// NewLog is the log version that chunk 0 of a step with no log creates. Log
// holds its ID and times, and the store sets Log.ItemKey from the key that
// NewKey creates for Ref in the write transaction.
type NewLog struct {
	Log    LogRef
	Ref    secret.ItemRef
	NewKey NewItemKeyFunc
}

// NewPlan is the plan that ReservePlan records before its parts exist. Plan
// holds its ID and times, and the store sets Plan.ItemKey from the key that
// NewKey creates for Ref in the write transaction.
type NewPlan struct {
	Plan   PlanRef
	Ref    secret.ItemRef
	NewKey NewItemKeyFunc
}

// ChunkReserve is one chunk of a log, as the store records it before the
// blob exists: the number, the SHA-256 and the size of its plaintext.
type ChunkReserve struct {
	Seq    int
	SHA256 string
	Size   int64
}

// ChunkRow is the stored record of one chunk of a log version. Confirmed is
// true when the blob of the chunk is stored and the chunk counts in
// LogRef.Chunks. BlobKey is the key of the blob of the winning attempt, as
// ChunkBlobKey reads it. It is empty for a chunk that is not confirmed.
type ChunkRow struct {
	SHA256    string
	Size      int64
	Confirmed bool
	BlobKey   string
}

// ChunkConfirm names the chunk that ConfirmLogChunk counts: the chunk Seq of
// the log version Version of the step Step.
type ChunkConfirm struct {
	Step    string
	Version string
	Seq     int
	// Stored is the size and the MD5 of the sealed bytes that the blob store
	// holds for the chunk, in the blob under BlobKey.
	Stored blob.Digest
	// BlobKey is the key of the blob of this write attempt, as
	// LogChunkAttemptKey makes it.
	BlobKey string
	// AttemptStartedAt is the time at which this write attempt started, before
	// its blob write, by the clock of the recorder. The store checks it with
	// blob.CheckAttempt against its own clock. The store does not keep it.
	AttemptStartedAt time.Time
}

// ChunkPlan is what PlanChunk decides for a reserve of a chunk.
type ChunkPlan struct {
	// Version is the ID of the log version that the chunk belongs to.
	Version string
	// CreateLog is true when the chunk creates the log version of the step.
	CreateLog bool
	// Insert is true when the store inserts the row of the chunk. It is
	// false for a chunk that a reserve recorded before.
	Insert bool
	// Duplicate is true when the chunk is stored and confirmed with the same
	// hash: the call changes nothing.
	Duplicate bool
}

// ArtifactRecord is what the store knows of one stored plan or one log
// version. Exactly one of Plan and Log is set. A log version also names its
// step.
type ArtifactRecord struct {
	Job  ID
	Unit identity.Object
	// Step is the step of a log version.
	Step string
	Plan *PlanRef
	Log  *LogRef
	// ChunkKeys is set for a log version that Log returns. Element seq is the
	// blob key of the confirmed chunk seq, as ChunkBlobKey reads it. The list
	// has one element for each confirmed chunk, in the order of their numbers.
	ChunkKeys []string
}

// RecordStore is the persistence port for the writes of a Recorder and for
// the lookup of stored plans and log versions. Each SQL store adapter
// implements it in its own SQL with the shared rules of this file, and runs
// the conformance suite in package jobtest.
//
// Every write checks the claim in its own transaction, as every write of an
// executor does: the namespace fence of pc is current, the job is running or
// canceling at the phase and with the operation of pc, and the claim is the
// live claim of that operation on the clock of the store. A write returns
// namespace.ErrFenced for a stale fence, ErrNotFound for an unknown job, and
// ErrClaimLost (with durable.ErrClaimLost) for a claim that is not live or a
// phase that the job is not at. It changes nothing on an error.
//
//nolint:interfacebloat // One port for the writes and the lookup of one record set: the recorder needs them all in one transaction each.
type RecordStore interface {
	// BeginStep records the begin of the step called name under the claim,
	// with the time of the store. It returns ErrCanceled while a cancel is
	// requested, durable.ErrStepDone when the claim recorded the end of the
	// step, and ErrInvalid for a name that the phase has no step for. A second
	// begin of an open step changes nothing.
	BeginStep(ctx context.Context, pc PhaseClaim, name string) error
	// EndStep records the end of the step called name, with the exit code and
	// the time of the store. When the step has a log version that is open,
	// closing closes it, and it must name that version and the chunks that the
	// store counts: else ErrConflict. It returns ErrInvalid for a step with
	// no begin record under the claim, for an exit code that is not in -1 to
	// MaxExitCode, and for closing with no log. A second end with the same
	// outcome changes nothing, and another outcome is ErrConflict.
	EndStep(ctx context.Context, pc PhaseClaim, name string, exit int, closing *LogClose) error
	// ReserveLogChunk records the chunk c of the log of the step called step
	// before its blob exists, and creates the log version newLog, with its item
	// key, when this is chunk 0 of a step with no log (newLog is otherwise
	// ignored). The key record commits in the same transaction. The
	// rules are in PlanChunk. It returns the plan, which names the version.
	ReserveLogChunk(ctx context.Context, pc PhaseClaim, step string, c ChunkReserve, newLog *NewLog) (ChunkPlan, error)
	// ConfirmLogChunk marks the chunk c as stored, and counts it in
	// LogRef.Chunks and LogRef.Size. The store keeps c.BlobKey and c.Stored,
	// the size and the MD5 of the sealed bytes in the blob store, in the row of
	// the chunk. The first confirm of a chunk wins: it returns true. A chunk
	// that is counted already changes nothing, and its blob key and stored
	// values stay. The call then returns true if the row holds c.BlobKey (the
	// call repeats a confirm that committed), and false if the row holds
	// another blob key: the blob of c is then not named by any row, and the
	// caller deletes it. It returns ErrInvalid for a chunk that no reserve
	// recorded or that is not the next one, for a c.Stored with no size, and
	// for a c.BlobKey that is empty or is not the key of an attempt of this
	// chunk. It returns blob.ErrAttemptExpired, and changes nothing, for a
	// c.AttemptStartedAt that fails blob.CheckAttempt against the clock of the
	// store, also for a chunk that another attempt counted. A repeat of a
	// confirm that the row holds with c.BlobKey returns true at any age, so the
	// caller never deletes a blob that a row names.
	ConfirmLogChunk(ctx context.Context, pc PhaseClaim, c ChunkConfirm) (bool, error)
	// ReservePlan records the plan np before its parts exist, with its item key
	// in the same transaction, and returns it. A reserved plan has no part and
	// is not readable. It applies the checks of PutPlan: when the current state
	// version of the unit is another one, it fails the phase in the same
	// transaction (with the end of the claim) and returns ErrPlanStale. It
	// returns ErrInvalid for a phase that is not a plan phase, and ErrConflict
	// for a phase that has a plan. A second reserve of the same plan ID by the
	// same job, before the parts exist, checks the claim again and returns the
	// reserved plan with no second item key. A reserve of the ID of a plan of
	// another job, or of a plan that has parts, returns ErrConflict.
	ReservePlan(ctx context.Context, pc PhaseClaim, np *NewPlan) (PlanRef, error)
	// PutPlan stores p, with its parts, as the plan of the plan phase of pc
	// that ReservePlan recorded, with the state version of the phase, and
	// returns it. It applies the checks of ReservePlan again, because a state
	// write can come between the two calls. It returns ErrNotFound for a plan
	// that no reserve recorded, and ErrConflict for a plan with parts.
	PutPlan(ctx context.Context, pc PhaseClaim, p PlanRef) (PlanRef, error)
	// Delivered records d as the delivery of the deliver phase of pc, and
	// appends e in the same transaction. A second record of the same result
	// changes nothing, and another result is ErrConflict. It returns ErrInvalid
	// for a phase that is not a deliver phase.
	Delivered(ctx context.Context, pc PhaseClaim, d DeliveryResult, e audit.Entry) error
	// Plan returns the stored plan with the ID plan, also one that a replan
	// cleared from its phase. It returns ErrNotFound for an unknown plan and
	// for a plan of another namespace.
	Plan(ctx context.Context, ns namespace.Name, plan string) (ArtifactRecord, error)
	// Log returns the stored log version with the ID version, with the same
	// rules as Plan.
	Log(ctx context.Context, ns namespace.Name, version string) (ArtifactRecord, error)
}

// StepKey names the record of one step under one claim: the phase of the job,
// the token of the claim and the name of the step.
type StepKey struct {
	Phase int
	Token int64
	Name  string
}

// stepWriteAllowed reports whether the phase at index phase of j may record
// the step called name: a step of the spec that runs in that phase, or a
// step of the write-back executor in a deliver phase.
func stepWriteAllowed(j *Job, phase int, name string) bool {
	if phase < 0 || phase >= len(j.Phases) {
		return false
	}

	kind := j.Phases[phase].Kind

	if kind == PhaseDeliver {
		return name == WriteBackStepCommit || name == WriteBackStepReview
	}

	return slices.ContainsFunc(j.Spec.Steps, func(s Step) bool { return s.Name == name && s.Phase == kind })
}

// recordIndex returns the index of the record of the step called name under
// the token in the steps of phase, and -1 when there is none.
func recordIndex(p *Phase, token int64, name string) int {
	return slices.IndexFunc(p.Steps, func(r StepRecord) bool { return r.Name == name && r.Token == token })
}

// BeginStepRecord applies the begin of the step k to j, at the time now. It reports
// whether j changed. The rules are the ones of RecordStore.BeginStep.
func BeginStepRecord(j *Job, k StepKey, now time.Time) (bool, error) {
	if !stepWriteAllowed(j, k.Phase, k.Name) {
		return false, fmt.Errorf("%w: phase %d of job %s has no step %q", ErrInvalid, k.Phase, j.ID, k.Name)
	}

	if j.State == StateCanceling {
		return false, fmt.Errorf("%w: job %s is canceling", ErrCanceled, j.ID)
	}

	p := &j.Phases[k.Phase]

	if i := recordIndex(p, k.Token, k.Name); i >= 0 {
		if !p.Steps[i].FinishedAt.IsZero() {
			return false, fmt.Errorf("%w: step %q of job %s ended under claim %d", durable.ErrStepDone, k.Name, j.ID,
				k.Token)
		}

		return false, nil
	}

	p.Steps = append(p.Steps, StepRecord{
		Name: k.Name, Token: k.Token, ExitCode: 0, StartedAt: now, FinishedAt: time.Time{}, Log: nil,
	})

	return true, nil
}

// EndStepRecord applies the end of the step k to j, at the time now. It reports whether j
// changed. The rules are the ones of RecordStore.EndStep.
func EndStepRecord(j *Job, k StepKey, exit int, closing *LogClose, now time.Time) (bool, error) {
	if exit < -1 || exit > MaxExitCode {
		return false, fmt.Errorf("%w: exit code %d is not in -1 to %d", ErrInvalid, exit, MaxExitCode)
	}

	if k.Phase < 0 || k.Phase >= len(j.Phases) {
		return false, fmt.Errorf("%w: job %s has no phase %d", ErrInvalid, j.ID, k.Phase)
	}

	p := &j.Phases[k.Phase]

	i := recordIndex(p, k.Token, k.Name)
	if i < 0 {
		return false, fmt.Errorf("%w: step %q has no begin record under claim %d", ErrInvalid, k.Name, k.Token)
	}

	r := &p.Steps[i]

	if !r.FinishedAt.IsZero() {
		if r.ExitCode == exit {
			return false, nil
		}

		return false, fmt.Errorf("%w: step %q ended with exit code %d, not %d", ErrConflict, k.Name, r.ExitCode, exit)
	}

	if err := closeLog(r, closing, now); err != nil {
		return false, err
	}

	r.ExitCode, r.FinishedAt = exit, now

	return true, nil
}

// closeLog closes the open log version of r with closing, at the time now.
func closeLog(r *StepRecord, closing *LogClose, now time.Time) error {
	open := r.Log != nil && r.Log.ClosedAt.IsZero()

	switch {
	case closing != nil && r.Log == nil:
		return fmt.Errorf("%w: step %q has no log to close", ErrInvalid, r.Name)
	case closing == nil && open:
		return fmt.Errorf("%w: step %q has an open log: %s", ErrConflict, r.Name, r.Log.ID)
	case closing == nil:
		return nil
	case !open:
		return fmt.Errorf("%w: the log of step %q is closed", ErrConflict, r.Name)
	case closing.Version != r.Log.ID || closing.Chunks != r.Log.Chunks:
		return fmt.Errorf("%w: the log of step %q has %d chunks, the close names %d of %q", ErrConflict, r.Name,
			r.Log.Chunks, closing.Chunks, closing.Version)
	case !ValidHash(closing.SHA256) || closing.Size < 0:
		return fmt.Errorf("%w: the close of the log of step %q has no valid hash or size", ErrInvalid, r.Name)
	}

	r.Log.SHA256, r.Log.Size, r.Log.ClosedAt = closing.SHA256, closing.Size, now

	return nil
}

// ValidHash reports whether h is a SHA-256 in lowercase hexadecimal, the form
// of every hash of a plan part and of a log.
func ValidHash(h string) bool {
	if len(h) != 2*sha256.Size {
		return false
	}

	for _, c := range []byte(h) {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}

	return true
}

// PlanChunk decides the reserve of the chunk c of the log of the step k of j.
// lookup reads the stored row of a chunk, and reports false when none exists.
// newLog is the log version that a chunk 0 creates when the step has no log.
// The rules:
//   - the step must have a begin record under the claim and no end record,
//   - the number must be 0 or more, the size 0 to MaxLogChunk and the hash a
//     SHA-256, else ErrInvalid,
//   - a step with no log takes chunk 0 only, and then newLog must be set,
//   - a closed log takes no chunk (ErrInvalid),
//   - a chunk with a stored row and the same hash is no change (Duplicate when
//     it is confirmed), and another hash is ErrConflict,
//   - a chunk with no row must be the next one (LogRef.Chunks) and below
//     MaxLogChunks, else ErrInvalid.
func PlanChunk(
	j *Job, k StepKey, c ChunkReserve, newLog *LogRef,
	lookup func(version string, seq int) (ChunkRow, bool, error),
) (ChunkPlan, error) {
	if !c.valid() {
		return ChunkPlan{}, fmt.Errorf("%w: chunk %d is not valid", ErrInvalid, c.Seq)
	}

	log, err := openLogOf(j, k)
	if err != nil {
		return ChunkPlan{}, err
	}

	if log == nil {
		return planFirstChunk(k.Name, c.Seq, newLog)
	}

	row, ok, err := lookup(log.ID, c.Seq)
	if err != nil {
		return ChunkPlan{}, err
	}

	return planNextChunk(log, k.Name, &c, row, ok)
}

// valid reports whether c has a number below MaxLogChunks, a size up to
// MaxLogChunk and a SHA-256.
func (c ChunkReserve) valid() bool {
	return c.Seq >= 0 && c.Seq < MaxLogChunks && c.Size >= 0 && c.Size <= MaxLogChunk && ValidHash(c.SHA256)
}

// planNextChunk decides the reserve of the chunk c of the open log, when the
// store holds the row (ok) or not.
func planNextChunk(log *LogRef, step string, c *ChunkReserve, row ChunkRow, ok bool) (ChunkPlan, error) {
	switch {
	case ok && row.SHA256 != c.SHA256:
		return ChunkPlan{}, fmt.Errorf("%w: chunk %d of step %q has another hash", ErrConflict, c.Seq, step)
	case ok:
		return ChunkPlan{Version: log.ID, CreateLog: false, Insert: false, Duplicate: row.Confirmed}, nil
	case c.Seq != log.Chunks:
		return ChunkPlan{}, fmt.Errorf("%w: chunk %d of step %q is not the next one, which is %d", ErrInvalid, c.Seq,
			step, log.Chunks)
	default:
		return ChunkPlan{Version: log.ID, CreateLog: false, Insert: true, Duplicate: false}, nil
	}
}

// openLogOf returns the log of the open step k of j, or nil when the step has
// none. It returns ErrInvalid for a step that is not open under the claim, and
// for a log that is closed.
func openLogOf(j *Job, k StepKey) (*LogRef, error) {
	if k.Phase < 0 || k.Phase >= len(j.Phases) {
		return nil, fmt.Errorf("%w: job %s has no phase %d", ErrInvalid, j.ID, k.Phase)
	}

	p := &j.Phases[k.Phase]

	i := recordIndex(p, k.Token, k.Name)
	if i < 0 || !p.Steps[i].FinishedAt.IsZero() {
		return nil, fmt.Errorf("%w: step %q is not open under claim %d", ErrInvalid, k.Name, k.Token)
	}

	log := p.Steps[i].Log
	if log != nil && !log.ClosedAt.IsZero() {
		return nil, fmt.Errorf("%w: the log of step %q is closed", ErrInvalid, k.Name)
	}

	return log, nil
}

// planFirstChunk decides the reserve of a chunk for a step with no log.
func planFirstChunk(step string, seq int, newLog *LogRef) (ChunkPlan, error) {
	switch {
	case seq != 0:
		return ChunkPlan{}, fmt.Errorf("%w: chunk %d of step %q comes before chunk 0", ErrInvalid, seq, step)
	case newLog == nil || newLog.ID == "" || newLog.Version != 1 || newLog.Chunks != 0 || !newLog.ClosedAt.IsZero():
		return ChunkPlan{}, fmt.Errorf("%w: chunk 0 of step %q needs a new open log version", ErrInvalid, step)
	default:
		return ChunkPlan{Version: newLog.ID, CreateLog: true, Insert: true, Duplicate: false}, nil
	}
}

// CreateLogRecord sets the new log version newLog on the open record of the
// step k of j.
// PlanChunk checked the step before.
func CreateLogRecord(j *Job, k StepKey, newLog *LogRef) {
	p := &j.Phases[k.Phase]

	log := *newLog
	p.Steps[recordIndex(p, k.Token, k.Name)].Log = &log
}

// ConfirmChunkRecord counts the chunk seq of the open log version of the step k
// of j, with the row of the chunk. It reports whether j changed. A chunk that
// is counted already changes nothing. It returns ErrInvalid for a log that is
// not the version, is closed, or when seq is above the next chunk.
func ConfirmChunkRecord(j *Job, k StepKey, version string, seq int, row ChunkRow) (bool, error) {
	if k.Phase < 0 || k.Phase >= len(j.Phases) {
		return false, fmt.Errorf("%w: job %s has no phase %d", ErrInvalid, j.ID, k.Phase)
	}

	p := &j.Phases[k.Phase]

	i := recordIndex(p, k.Token, k.Name)
	if i < 0 || p.Steps[i].Log == nil || p.Steps[i].Log.ID != version || !p.Steps[i].Log.ClosedAt.IsZero() {
		return false, fmt.Errorf("%w: step %q has no open log %q under claim %d", ErrInvalid, k.Name, version, k.Token)
	}

	log := p.Steps[i].Log

	switch {
	case seq < log.Chunks:
		return false, nil
	case seq > log.Chunks:
		return false, fmt.Errorf("%w: chunk %d of step %q is not the next one, which is %d", ErrInvalid, seq, k.Name,
			log.Chunks)
	}

	log.Chunks++
	log.Size += row.Size

	return true, nil
}

// CheckPutPlan returns the error of the PutPlan of the phase at index phase
// of j while current is the current state version of the unit: ErrInvalid for
// a phase that is not a plan phase, ErrConflict for a phase that has a plan
// already, and ErrPlanStale (naming both versions) when the plan phase read
// another state version than current.
func CheckPutPlan(j *Job, phase int, current string) error {
	if phase < 0 || phase >= len(j.Phases) || j.Phases[phase].Kind != PhasePlan {
		return fmt.Errorf("%w: phase %d of job %s is not a plan phase", ErrInvalid, phase, j.ID)
	}

	p := &j.Phases[phase]

	switch {
	case p.Plan != nil:
		return fmt.Errorf("%w: phase %d of job %s has plan %s", ErrConflict, phase, j.ID, p.Plan.ID)
	case p.StateVersion != current:
		return fmt.Errorf("%w: the plan phase read state version %q, the current one is %q", ErrPlanStale,
			p.StateVersion, current)
	default:
		return nil
	}
}

// CheckPlanParts returns ErrInvalid unless parts are the parts of a plan: one
// or two parts, each of a plan output kind, no kind twice, each with a valid
// SHA-256 and a size from 0 to MaxPlanPart, and only a part of kind
// OutputPlanApply is secret.
func CheckPlanParts(parts []PlanPart) error {
	if len(parts) < 1 || len(parts) > 2 {
		return fmt.Errorf("%w: a plan has 1 or 2 parts, not %d", ErrInvalid, len(parts))
	}

	seen := make(map[OutputKind]bool, len(parts))

	for i := range parts {
		p := &parts[i]

		switch {
		case p.Kind != OutputPlanDisplay && p.Kind != OutputPlanApply:
			return fmt.Errorf("%w: %q is not a plan output", ErrInvalid, p.Kind)
		case seen[p.Kind]:
			return fmt.Errorf("%w: plan part %q twice", ErrInvalid, p.Kind)
		case !ValidHash(p.SHA256):
			return fmt.Errorf("%w: plan part %q has no valid hash", ErrInvalid, p.Kind)
		case p.Size < 0 || p.Size > MaxPlanPart:
			return fmt.Errorf("%w: plan part %q has a size from 0 to %d, not %d", ErrInvalid, p.Kind, MaxPlanPart,
				p.Size)
		case p.Secret != (p.Kind == OutputPlanApply):
			return fmt.Errorf("%w: plan part %q has secret %t, not %t", ErrInvalid, p.Kind, p.Secret,
				p.Kind == OutputPlanApply)
		}

		if err := checkStoredPart(p); err != nil {
			return err
		}

		seen[p.Kind] = true
	}

	return nil
}

// checkStoredPart checks the stored size and the MD5 of a plan part: both or
// neither. A plan from an earlier version of the server has neither. A part
// with an MD5 has a stored size above zero and an MD5 of 32 lowercase hex
// characters.
func checkStoredPart(p *PlanPart) error {
	if p.StoredMD5 == "" && p.StoredSize == 0 {
		return nil
	}

	if p.StoredSize <= 0 || !ValidMD5(p.StoredMD5) {
		return fmt.Errorf("%w: plan part %q has no valid stored size and MD5", ErrInvalid, p.Kind)
	}

	return nil
}

// md5HexLength is the length of an MD5 in hexadecimal.
const md5HexLength = 32

// ValidMD5 reports whether h is an MD5 in lowercase hexadecimal.
func ValidMD5(h string) bool {
	if len(h) != md5HexLength {
		return false
	}

	for _, c := range []byte(h) {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}

	return true
}

// CheckDelivered returns the error of the record of d as the delivery of the
// phase at index phase of j, and reports whether it changes j. It returns
// ErrInvalid for a phase that is not a deliver phase and for a result with no
// commit, no branch or a delivery kind that is unknown. It returns ErrConflict
// for a job that has another result.
func CheckDelivered(j *Job, phase int, d *DeliveryResult) (bool, error) {
	if phase < 0 || phase >= len(j.Phases) || j.Phases[phase].Kind != PhaseDeliver {
		return false, fmt.Errorf("%w: phase %d of job %s is not a deliver phase", ErrInvalid, phase, j.ID)
	}

	if err := d.Validate(); err != nil {
		return false, err
	}

	if j.Delivery == nil {
		return true, nil
	}

	if !j.Delivery.Same(d) {
		return false, fmt.Errorf("%w: job %s has another delivery", ErrConflict, j.ID)
	}

	return false, nil
}

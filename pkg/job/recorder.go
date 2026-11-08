package job

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"log/slog"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/history"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
)

// RecorderConfig holds the dependencies of a DurableRecorder. Every field is
// required but Now.
type RecorderConfig struct {
	// Store holds the records of steps, logs, plans and deliveries.
	Store RecordStore
	// Jobs reads a job, for the checks that come before a blob write.
	Jobs Store
	// Blobs stores the sealed content of plans and logs.
	Blobs *ArtifactBlobs
	// Sealer makes the item key of each plan and log version and seals their
	// content.
	Sealer     secret.ItemSealer
	Namespaces NamespaceReader
	// Retention gives the end of the retention period of each new plan and log
	// version.
	Retention history.RetentionSettings
	// Now is the time source. The default is time.Now.
	Now func() time.Time
	// Log receives the warnings of the recorder. It is required.
	Log *slog.Logger
}

// DurableRecorder is the Recorder over a RecordStore, the blob store and the
// item keys. Every method checks the claim in the transaction of its write
// (RecordStore), so a holder that lost its claim to a takeover records
// nothing. The item key of a plan or a log version commits in the transaction
// that stores the plan or the log version, so the record of the key never
// exists without its item. The sealed content comes after that, because the
// sealer reads the committed record, and the record that counts the content
// comes last. A write that fails after its blobs leaves orphan blobs, which
// the artifact sweep (ArtifactSweeper) deletes after a grace period. PutPlan
// also deletes them at once.
type DurableRecorder struct {
	store      RecordStore
	jobs       Store
	blobs      *ArtifactBlobs
	sealer     secret.ItemSealer
	namespaces NamespaceReader
	retention  history.RetentionSettings
	now        func() time.Time
	log        *slog.Logger
}

var _ Recorder = (*DurableRecorder)(nil)

// NewRecorder returns the recorder of the progress of phases.
func NewRecorder(cfg RecorderConfig) (*DurableRecorder, error) {
	switch {
	case cfg.Store == nil:
		return nil, fmt.Errorf("%w: record store", ErrInvalid)
	case cfg.Jobs == nil:
		return nil, fmt.Errorf("%w: job store", ErrInvalid)
	case cfg.Blobs == nil:
		return nil, fmt.Errorf("%w: artifact blobs", ErrInvalid)
	case cfg.Sealer == nil:
		return nil, fmt.Errorf("%w: item sealer", ErrInvalid)
	case cfg.Namespaces == nil:
		return nil, fmt.Errorf("%w: namespace reader", ErrInvalid)
	case cfg.Log == nil:
		return nil, fmt.Errorf("%w: logger", ErrInvalid)
	}

	if err := cfg.Retention.Validate(); err != nil {
		return nil, fmt.Errorf("%w: retention: %w", ErrInvalid, err)
	}

	now := cfg.Now
	if now == nil {
		now = time.Now
	}

	return &DurableRecorder{
		store: cfg.Store, jobs: cfg.Jobs, blobs: cfg.Blobs, sealer: cfg.Sealer, namespaces: cfg.Namespaces,
		retention: cfg.Retention, now: now, log: cfg.Log,
	}, nil
}

// Retention answers the retention periods that the recorder gives to each
// new plan and each new version of a job log.
func (r *DurableRecorder) Retention() history.RetentionSettings {
	return r.retention
}

// BeginStep implements Recorder.
func (r *DurableRecorder) BeginStep(ctx context.Context, c PhaseClaim, name string) error {
	if err := checkStepName(name); err != nil {
		return err
	}

	return r.store.BeginStep(ctx, c, name)
}

// checkStepName returns ErrInvalid for a name that is not the name of a step.
func checkStepName(name string) error {
	if len(name) > MaxStepName || !stepNamePattern.MatchString(name) {
		return fmt.Errorf("%w: step name %q", ErrInvalid, name)
	}

	return nil
}

// EndStep implements Recorder. It uses the name, the token and the exit code
// of rec. The times and the log come from the server: the begin time is the
// one the store recorded, the end time is the time of the store, and the log
// version is the one that the chunks made.
func (r *DurableRecorder) EndStep(ctx context.Context, c PhaseClaim, rec StepRecord) error {
	return r.finishStep(ctx, c, rec, nil)
}

// LogExpectation is the log that a caller says a step ends with: the number of
// chunks and the hex SHA-256 of the whole plaintext. The SHA-256 is empty for a
// step with no chunk.
type LogExpectation struct {
	Chunks int
	SHA256 string
}

// CheckedRecorder is a Recorder that ends a step only when the log of the step
// is the log that the caller expects.
type CheckedRecorder interface {
	Recorder
	// EndStepChecked is EndStep with a check that comes before the write: when
	// the log that the chunks made is not the log of want, it returns
	// ErrConflict and changes nothing, so the step stays open and its log stays
	// open. A step that has no begin record under c is ErrInvalid, as for
	// EndStep. A retry after a successful end compares the stored closed log.
	EndStepChecked(ctx context.Context, c PhaseClaim, rec StepRecord, want LogExpectation) error
}

var _ CheckedRecorder = (*DurableRecorder)(nil)

// EndStepChecked implements CheckedRecorder.
func (r *DurableRecorder) EndStepChecked(
	ctx context.Context, c PhaseClaim, rec StepRecord, want LogExpectation,
) error {
	return r.finishStep(ctx, c, rec, &want)
}

// stepRecordOf returns the record of the step called name under the claim c
// in the phase of c.
func stepRecordOf(j *Job, c PhaseClaim, name string) (*StepRecord, bool) {
	if c.Phase < 0 || c.Phase >= len(j.Phases) {
		return nil, false
	}

	p := &j.Phases[c.Phase]

	i := recordIndex(p, c.Claim.Token, name)
	if i < 0 {
		return nil, false
	}

	return &p.Steps[i], true
}

// AppendLog implements Recorder. A chunk costs one read of the job, one write
// that reserves the chunk, one blob and one write that counts it. The
// reserve decides every conflict about the content of the chunk. Each attempt
// writes the blob under a key of its own, so two attempts of one chunk never
// write one key, also on a blob store that replaces an object. The write that
// counts the chunk decides which attempt wins: the row names the blob of the
// first attempt that commits, and an attempt that loses deletes only its own
// blob.
func (r *DurableRecorder) AppendLog(ctx context.Context, c PhaseClaim, step string, seq int, chunk io.Reader) error {
	if err := checkStepName(step); err != nil {
		return err
	}

	if seq < 0 || seq >= MaxLogChunks {
		return fmt.Errorf("%w: chunk number %d", ErrInvalid, seq)
	}

	data, sum, err := readChunk(chunk)
	if err != nil {
		return err
	}

	j, err := r.jobOf(ctx, c)
	if err != nil {
		return err
	}

	newLog, err := r.newLogVersion(&j, c, step, seq)
	if err != nil {
		return err
	}

	plan, err := r.store.ReserveLogChunk(
		ctx,
		c,
		step,
		ChunkReserve{Seq: seq, SHA256: sum, Size: int64(len(data))},
		newLog,
	)
	if err != nil {
		return err
	}

	if plan.Duplicate {
		return nil
	}

	return r.writeChunk(ctx, c, &j, &chunkWrite{step: step, version: plan.Version, seq: seq, data: data})
}

// readChunk reads a chunk of at most MaxLogChunk bytes, and returns it with
// its SHA-256. A longer chunk is ErrInvalid.
func readChunk(chunk io.Reader) ([]byte, string, error) {
	data, err := io.ReadAll(io.LimitReader(chunk, MaxLogChunk+1))
	if err != nil {
		return nil, "", fmt.Errorf("read log chunk: %w", err)
	}

	if len(data) > MaxLogChunk {
		return nil, "", fmt.Errorf("%w: a log chunk has at most %d bytes", ErrInvalid, MaxLogChunk)
	}

	sum := sha256.Sum256(data)

	return data, hex.EncodeToString(sum[:]), nil
}

// PutPlan implements Recorder. The part of kind OutputPlanApply gets the flag
// Secret: it is the plan that the tool reads back, and it can hold plain
// secret values. The hash and the size of each part are those of its
// plaintext, which the recorder counts while it seals.
func (r *DurableRecorder) PutPlan(ctx context.Context, c PhaseClaim, parts []PlanUpload) (PlanRef, error) {
	if err := checkPlanUploads(parts); err != nil {
		return PlanRef{}, err
	}

	j, err := r.jobOf(ctx, c)
	if err != nil {
		return PlanRef{}, err
	}

	// A cheap check before the upload: the store checks the same in its
	// transaction.
	if c.Phase < 0 || c.Phase >= len(j.Phases) {
		return PlanRef{}, fmt.Errorf("%w: job %s has no phase %d", ErrInvalid, j.ID, c.Phase)
	}

	// Passing the state version of the phase as the current one leaves the
	// stale check to the store, which reads the current version.
	if checkErr := CheckPutPlan(&j, c.Phase, j.Phases[c.Phase].StateVersion); checkErr != nil {
		return PlanRef{}, checkErr
	}

	planID := ident.NewID(PlanIDPrefix)
	art := ArtifactRecord{Job: j.ID, Unit: j.Spec.Unit, Step: "", Plan: nil, Log: nil, ChunkKeys: nil}

	ref, err := itemRef(&art, secret.ItemKindPlan, planID)
	if err != nil {
		return PlanRef{}, err
	}

	now := r.now().UTC()

	// The reserve commits the plan row and the record of its item key in one
	// transaction. The parts seal after it, because the sealer reads the
	// committed record of the key.
	reserved, err := r.store.ReservePlan(ctx, c, &NewPlan{
		Plan: PlanRef{
			ID: planID, ItemKey: "", StateVersion: "", Parts: nil, CreatedAt: now,
			ExpiresAt: history.ExpiresAt(now, r.retention.Plan), RedactedAt: time.Time{},
		},
		Ref:    ref,
		NewKey: r.sealer.NewItemKey,
	})
	if err != nil {
		return PlanRef{}, fmt.Errorf("reserve plan %s: %w", planID, err)
	}

	nsID, err := namespaceIDOf(ctx, r.namespaces, c.Fence.Namespace)
	if err != nil {
		return PlanRef{}, err
	}

	at := &planAt{ns: nsID, job: j.ID, plan: planID, item: ref}

	stored, err := r.storeParts(ctx, at, parts)
	if err != nil {
		return PlanRef{}, err
	}

	plan := reserved
	plan.Parts = stored

	out, err := r.store.PutPlan(ctx, c, plan)
	if err != nil {
		// An error that does not prove a rollback, such as a failed COMMIT,
		// can follow a commit: the plan row may name the parts, so the
		// parts stay.
		if provesRollback(err) {
			r.deleteParts(ctx, at, stored)
		}

		return PlanRef{}, err
	}

	return out, nil
}

// provesRollback reports whether err from the store proves that the store did
// not commit the write. Only the errors that the store returns before it
// commits do. Any other error, such as a failure of the COMMIT, can follow a
// commit that the server made.
func provesRollback(err error) bool {
	for _, target := range []error{
		ErrPlanStale, ErrConflict, ErrClaimLost, namespace.ErrFenced, ErrNotFound, ErrInvalid,
		blob.ErrAttemptExpired,
	} {
		if errors.Is(err, target) {
			return true
		}
	}

	return false
}

// checkPlanUploads returns ErrInvalid for no part, a kind that is not a plan
// output, or a kind twice.
func checkPlanUploads(parts []PlanUpload) error {
	if len(parts) == 0 {
		return fmt.Errorf("%w: a plan needs at least one part", ErrInvalid)
	}

	seen := make(map[OutputKind]bool, len(parts))

	for _, p := range parts {
		switch {
		case p.Kind != OutputPlanDisplay && p.Kind != OutputPlanApply:
			return fmt.Errorf("%w: %q is not a plan output", ErrInvalid, p.Kind)
		case seen[p.Kind]:
			return fmt.Errorf("%w: plan part %q twice", ErrInvalid, p.Kind)
		case p.Body == nil:
			return fmt.Errorf("%w: plan part %q has no content", ErrInvalid, p.Kind)
		}

		seen[p.Kind] = true
	}

	return nil
}

// countingReader counts and hashes what it reads, and fails with ErrInvalid
// when the content is longer than limit.
type countingReader struct {
	src   io.Reader
	sum   hash.Hash
	size  int64
	limit int64
}

// Read implements io.Reader.
func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.src.Read(p)
	c.size += int64(n)
	_, _ = c.sum.Write(p[:n])

	if c.size > c.limit {
		return n, fmt.Errorf("%w: a plan part has at most %d bytes", ErrInvalid, c.limit)
	}

	return n, err //nolint:wrapcheck // io.EOF must pass as it is.
}

// Delivered implements Recorder. It writes the audit entry of the delivery of
// a write-back (action unit:write-back) in the transaction of the record.
func (r *DurableRecorder) Delivered(ctx context.Context, c PhaseClaim, d DeliveryResult) error {
	if err := d.Validate(); err != nil {
		return err
	}

	j, err := r.jobOf(ctx, c)
	if err != nil {
		return err
	}

	unit := j.Spec.Unit

	return r.store.Delivered(
		ctx,
		c,
		d,
		deliveryEntry(&j, unit, &d, c, r.now().UTC()),
	)
}

// chunkWrite is one attempt to write the chunk seq of the log version of the
// step.
type chunkWrite struct {
	step    string
	version string
	seq     int
	data    []byte
}

// writeChunk stores the blob of one attempt of a chunk under a key of its own,
// and counts the chunk. If another attempt counted the chunk first, it deletes
// the blob of this attempt.
func (r *DurableRecorder) writeChunk(ctx context.Context, c PhaseClaim, j *Job, w *chunkWrite) error {
	key, err := LogChunkAttemptKey(j.ID, w.version, w.seq, ident.NewID(LogAttemptIDPrefix))
	if err != nil {
		return err
	}

	ref := LogChunkRef{Namespace: "", Job: j.ID, Version: w.version, Seq: w.seq, BlobKey: key.String()}

	// The attempt starts before the blob write. The store refuses a confirm
	// after the attempt deadline, so the artifact sweep, which waits longer
	// than the deadline, never deletes a blob that a later confirm names.
	started := r.now().UTC()

	stored, err := r.storeChunk(ctx, c, j, &ref, w.data)
	if err != nil {
		return err
	}

	won, err := r.store.ConfirmLogChunk(ctx, c, ChunkConfirm{
		Step: w.step, Version: w.version, Seq: w.seq, Stored: stored, BlobKey: ref.BlobKey,
		AttemptStartedAt: started,
	})
	if err != nil {
		// An error that does not prove a rollback, such as a failed COMMIT,
		// can follow a commit: the row may name the blob, so the blob stays.
		// If the commit did not happen, the artifact sweep deletes it.
		if provesRollback(err) {
			r.deleteChunk(ctx, &ref)
		}

		return err
	}

	if !won {
		// Another attempt of the chunk committed first, and its row names its
		// own blob. The blob of this attempt is named by no row.
		r.deleteChunk(ctx, &ref)
	}

	return nil
}

// deleteChunk deletes the blob of the attempt ref, which no row names. It does
// not stop when the context ends, and it ignores a failure: the artifact sweep
// deletes what stays. It deletes only the key of its own attempt.
func (r *DurableRecorder) deleteChunk(ctx context.Context, ref *LogChunkRef) {
	_ = r.blobs.DeleteLogChunk(context.WithoutCancel(ctx), *ref) //nolint:errcheck // See above.
}

// deliveryEntry returns the audit entry of a delivery. It holds the commit and
// the review request, and no secret.
func deliveryEntry(j *Job, unit identity.Object, d *DeliveryResult, c PhaseClaim, at time.Time) audit.Entry {
	return audit.Entry{
		At: at, Namespace: unit.Namespace(), Subject: c.Claim.Owner, IDPSubject: "", TokenID: "",
		Action: string(ActionUnitWriteBack), Object: unit.String(), Version: d.Commit, ValuePath: "", ValueCount: nil,
		Changes: nil, SourceIP: "", UserAgent: "", Channel: audit.ChannelAPI, Kind: audit.KindChange,
		Outcome: audit.OutcomeSuccess, RequestID: "", ObjectID: unit.UnitID(), PrevHash: "", Hash: "", Sequence: 0,
		FenceVersion: 0, PrincipalKind: audit.PrincipalKindService, JobID: string(j.ID), Reason: d.AuditReason(),
	}
}

// finishStep ends the step of rec. With want, it compares the log before the
// write of the store.
func (r *DurableRecorder) finishStep(ctx context.Context, c PhaseClaim, rec StepRecord, want *LogExpectation) error {
	if err := checkStepName(rec.Name); err != nil {
		return err
	}

	if rec.Token != c.Claim.Token {
		return fmt.Errorf("%w: the record of step %q carries token %d, the claim has token %d", ErrInvalid, rec.Name,
			rec.Token, c.Claim.Token)
	}

	closing, err := r.closeLog(ctx, c, rec.Name)
	if err != nil {
		return err
	}

	if want != nil {
		if checkErr := r.compareLog(ctx, c, rec.Name, closing, *want); checkErr != nil {
			return checkErr
		}
	}

	return r.store.EndStep(ctx, c, rec.Name, rec.ExitCode, closing)
}

// compareLog compares the log that the end of the step closes with want. The
// closing record is nil when the step has no open log: the log is then the
// closed log of an earlier end of the step, or no log. A step with no record
// passes: the store refuses it.
func (r *DurableRecorder) compareLog(
	ctx context.Context, c PhaseClaim, name string, closing *LogClose, want LogExpectation,
) error {
	var (
		chunks int
		sum    string
	)

	if closing != nil {
		chunks, sum = closing.Chunks, closing.SHA256
	} else {
		j, err := r.jobOf(ctx, c)
		if err != nil {
			return err
		}

		var found bool

		if chunks, sum, found = closedLogOf(&j, c, name); !found {
			return nil
		}
	}

	if chunks != want.Chunks || (chunks > 0 && sum != want.SHA256) {
		return fmt.Errorf("log of step %q: %w", name, ErrLogDiffers)
	}

	return nil
}

// closedLogOf returns the number of chunks and the SHA-256 of the log of the
// step called name in j, or 0 and "" for a step with no log. Found is false
// for a step with no record.
func closedLogOf(j *Job, c PhaseClaim, name string) (int, string, bool) {
	rec, ok := stepRecordOf(j, c, name)
	if !ok {
		return 0, "", false
	}

	if rec.Log == nil {
		return 0, "", true
	}

	return rec.Log.Chunks, rec.Log.SHA256, true
}

// closeLog reads the open log version of the step called name back, and
// returns what closes it: the chunk count that the store holds, the hash of
// the plaintext in order, and its size. It returns nil for a step with no open
// log.
func (r *DurableRecorder) closeLog(ctx context.Context, c PhaseClaim, name string) (*LogClose, error) {
	j, err := r.jobOf(ctx, c)
	if err != nil {
		return nil, err
	}

	rec, ok := stepRecordOf(&j, c, name)
	if !ok || rec.Log == nil || !rec.Log.ClosedAt.IsZero() {
		return nil, nil //nolint:nilnil // No open log is a normal case: the store checks the step.
	}

	nsID, err := namespaceIDOf(ctx, r.namespaces, c.Fence.Namespace)
	if err != nil {
		return nil, err
	}

	// The keys of the chunks come from the store: the job record counts the
	// chunks, and the chunk rows name their blobs.
	stored, err := r.store.Log(ctx, c.Fence.Namespace, rec.Log.ID)
	if err != nil {
		return nil, fmt.Errorf("read log %s: %w", rec.Log.ID, err)
	}

	art := ArtifactRecord{
		Job:       j.ID,
		Unit:      j.Spec.Unit,
		Step:      name,
		Plan:      nil,
		Log:       rec.Log,
		ChunkKeys: stored.ChunkKeys,
	}

	rc, err := openLogVersion(ctx, r.blobs, r.sealer, nsID, &art)
	if err != nil {
		return nil, err
	}

	h := sha256.New()

	size, copyErr := io.Copy(h, rc)
	closeErr := rc.Close()

	if err := errors.Join(copyErr, closeErr); err != nil {
		return nil, fmt.Errorf("read log %s: %w", rec.Log.ID, err)
	}

	return &LogClose{
		Version: rec.Log.ID,
		Chunks:  rec.Log.Chunks,
		SHA256:  hex.EncodeToString(h.Sum(nil)),
		Size:    size,
	}, nil
}

// jobOf reads the job of c and checks that it is a job of the namespace of the
// fence of c.
//
// Defense in depth: the store checks the fence and the namespace of the job in
// the transaction of each write. This check stays so that a read that comes
// before a write never uses a job of another namespace.
func (r *DurableRecorder) jobOf(ctx context.Context, c PhaseClaim) (Job, error) {
	j, err := r.jobs.Get(ctx, c.Job)
	if err != nil {
		return Job{}, fmt.Errorf("read job %s: %w", c.Job, err)
	}

	if j.Spec.Unit.Namespace() != c.Fence.Namespace {
		return Job{}, fmt.Errorf("read job %s: %w", c.Job, ErrNotFound)
	}

	return j, nil
}

// newLogVersion returns the log version that chunk 0 of a step with no log
// creates, with the function that makes its item key in the transaction of
// the store. It returns nil when the step has a log or the chunk is not chunk
// 0: the store then decides.
func (r *DurableRecorder) newLogVersion(j *Job, c PhaseClaim, step string, seq int) (*NewLog, error) {
	rec, ok := stepRecordOf(j, c, step)
	if !ok || rec.Log != nil || seq != 0 {
		return nil, nil //nolint:nilnil // The store decides a chunk that is not a first chunk.
	}

	version := ident.NewID(LogVersionIDPrefix)

	art := ArtifactRecord{Job: j.ID, Unit: j.Spec.Unit, Step: step, Plan: nil, Log: nil, ChunkKeys: nil}

	ref, err := itemRef(&art, secret.ItemKindJobLog, version)
	if err != nil {
		return nil, err
	}

	now := r.now().UTC()

	return &NewLog{
		Log: LogRef{
			ID: version, ItemKey: "", Version: 1, Chunks: 0, SHA256: "", Size: 0, CreatedAt: now,
			ClosedAt: time.Time{}, ExpiresAt: history.ExpiresAt(now, r.retention.JobLog), RedactedAt: time.Time{},
			ReplacedBy: "", Matches: 0,
		},
		Ref:    ref,
		NewKey: r.sealer.NewItemKey,
	}, nil
}

// storeChunk seals the chunk ref and stores its blob under ref.BlobKey, and
// returns the size and the MD5 of the bytes that the blob store holds. The key
// belongs to this attempt alone, so a blob store that refuses a second write
// of a key (blob.ErrExists) does not refuse this one. ref.Namespace is set
// here.
func (r *DurableRecorder) storeChunk(
	ctx context.Context, c PhaseClaim, j *Job, ref *LogChunkRef, data []byte,
) (blob.Digest, error) {
	art := ArtifactRecord{Job: j.ID, Unit: j.Spec.Unit, Step: "", Plan: nil, Log: nil, ChunkKeys: nil}

	item, err := itemRef(&art, secret.ItemKindJobLog, ref.Version)
	if err != nil {
		return blob.Digest{}, err
	}

	binding, err := chunkBinding(j.ID, ref.Version, ref.Seq, ref.BlobKey)
	if err != nil {
		return blob.Digest{}, err
	}

	sealed, err := r.sealer.SealContent(ctx, item, binding, bytes.NewReader(data))
	if err != nil {
		return blob.Digest{}, fmt.Errorf("seal log chunk: %w", err)
	}

	ref.Namespace, err = namespaceIDOf(ctx, r.namespaces, c.Fence.Namespace)
	if err != nil {
		return blob.Digest{}, err
	}

	measured := blob.NewDigestReader(sealed)

	if putErr := r.blobs.PutLogChunk(ctx, *ref, measured); putErr != nil {
		// The write may have stored a part of the blob. No row names it.
		r.deleteChunk(ctx, ref)

		return blob.Digest{}, fmt.Errorf("store log chunk: %w", putErr)
	}

	return measured.Digest(), nil
}

// planAt names the plan whose parts the recorder stores: the namespace and the
// job that hold it, its ID, and the item key that seals it.
type planAt struct {
	ns   namespace.ID
	job  ID
	plan string
	item secret.ItemRef
}

// part returns the reference of the part k of the plan.
func (p *planAt) part(k OutputKind) PlanPartRef {
	return PlanPartRef{Namespace: p.ns, Job: p.job, Plan: p.plan, Kind: k}
}

// storeParts seals and stores each part, and returns its metadata.
func (r *DurableRecorder) storeParts(ctx context.Context, at *planAt, parts []PlanUpload) ([]PlanPart, error) {
	stored := make([]PlanPart, 0, len(parts))

	for _, p := range parts {
		key, err := PlanPartKey(at.job, at.plan, p.Kind)
		if err != nil {
			r.deleteParts(ctx, at, stored)

			return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
		}

		counted := &countingReader{src: p.Body, sum: sha256.New(), size: 0, limit: MaxPlanPart}

		digest, err := r.putPart(ctx, at, p.Kind, key, counted)
		if err != nil {
			r.deleteParts(ctx, at, stored)

			return nil, fmt.Errorf("store plan part %q: %w", p.Kind, err)
		}

		stored = append(stored, PlanPart{
			Kind: p.Kind, Size: counted.size, SHA256: hex.EncodeToString(counted.sum.Sum(nil)),
			Secret: p.Kind == OutputPlanApply, StoredSize: digest.Size, StoredMD5: hex.EncodeToString(digest.MD5[:]),
		})
	}

	return stored, nil
}

// putPart seals the body of the part k and stores it under key, and returns the
// size and the MD5 of the sealed bytes that the blob store holds.
func (r *DurableRecorder) putPart(
	ctx context.Context, at *planAt, k OutputKind, key blob.Key, body io.Reader,
) (blob.Digest, error) {
	sealed, err := r.sealer.SealContent(ctx, at.item, []byte(key.String()), body)
	if err != nil {
		return blob.Digest{}, fmt.Errorf("seal plan part: %w", err)
	}

	measured := blob.NewDigestReader(sealed)
	if err = r.blobs.PutPlanPart(ctx, at.part(k), measured); err != nil {
		return blob.Digest{}, fmt.Errorf("put plan part: %w", err)
	}

	return measured.Digest(), nil
}

// deleteParts deletes the blobs of the stored parts of a plan that was not
// stored. It does not stop when the context ends, and it ignores a failure:
// the artifact sweep deletes what stays.
func (r *DurableRecorder) deleteParts(ctx context.Context, at *planAt, parts []PlanPart) {
	ctx = context.WithoutCancel(ctx)

	for _, p := range parts {
		if err := r.blobs.DeletePlanPart(ctx, at.part(p.Kind)); err != nil {
			// The artifact sweep deletes the part later. The warning names the
			// part that stays, so that an operator can find it.
			r.log.WarnContext(ctx, "delete of a plan part that was not stored failed",
				slog.String("job_id", string(at.job)), slog.String("plan", at.plan),
				slog.String("kind", string(p.Kind)), slog.String("error", err.Error()))
		}
	}
}

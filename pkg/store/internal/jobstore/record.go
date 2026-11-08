package jobstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
)

var _ job.RecordStore = (*Store)(nil)

// claimed loads the job of pc in the transaction tx and checks the claim, as
// every write of an executor does: the job is running or canceling at the phase
// of pc with the operation of its claim, and the claim is the live claim of
// that operation on the clock of the database. The fence is checked by the
// runner before fn runs.
func claimed(ctx context.Context, tx Tx, pc job.PhaseClaim) (job.Job, error) {
	j, err := loadJob(ctx, tx, pc.Job, pc.Fence.Namespace)
	if err != nil {
		return job.Job{}, err
	}

	if !runsPhase(&j, pc) {
		return job.Job{}, fmt.Errorf("%w: %w: job %s is %s at phase %d", job.ErrClaimLost, durable.ErrClaimLost, j.ID,
			j.State, j.Current)
	}

	if claimErr := tx.CheckClaim(ctx, pc.Claim); claimErr != nil {
		return job.Job{}, fmt.Errorf("claim of job %s: %w: %w", j.ID, job.ErrClaimLost, claimErr)
	}

	return j, nil
}

// save replaces the row of j, which was read at version prev, with the next
// version, at the time of the database and strictly after the stored update
// time of the row.
func save(ctx context.Context, tx Tx, j *job.Job) error {
	now, err := tx.Now(ctx)
	if err != nil {
		return err
	}

	prev := j.Version
	j.UpdatedAt = nextUpdateAt(now, j.UpdatedAt)
	j.Version = prev + 1

	return update(ctx, tx, j, prev)
}

// stepKey returns the key of the record of the step called name under the claim
// pc.
func stepKey(pc *job.PhaseClaim, name string) job.StepKey {
	return job.StepKey{Phase: pc.Phase, Token: pc.Claim.Token, Name: name}
}

// BeginStep implements job.RecordStore.
func (s *Store) BeginStep(ctx context.Context, pc job.PhaseClaim, name string) error {
	return s.run.Write(ctx, pc.Fence, func(tx Tx) error {
		j, err := claimed(ctx, tx, pc)
		if err != nil {
			return err
		}

		now, err := tx.Now(ctx)
		if err != nil {
			return err
		}

		changed, err := job.BeginStepRecord(&j, stepKey(&pc, name), now)
		if err != nil || !changed {
			return err
		}

		return save(ctx, tx, &j)
	})
}

// EndStep implements job.RecordStore.
func (s *Store) EndStep(
	ctx context.Context, pc job.PhaseClaim, name string, exit int, closing *job.LogClose,
) error {
	return s.run.Write(ctx, pc.Fence, func(tx Tx) error {
		j, err := claimed(ctx, tx, pc)
		if err != nil {
			return err
		}

		now, err := tx.Now(ctx)
		if err != nil {
			return err
		}

		changed, err := job.EndStepRecord(&j, stepKey(&pc, name), exit, closing, now)
		if err != nil || !changed {
			return err
		}

		return save(ctx, tx, &j)
	})
}

// ReserveLogChunk implements job.RecordStore.
func (s *Store) ReserveLogChunk(
	ctx context.Context, pc job.PhaseClaim, step string, c job.ChunkReserve, newLog *job.NewLog,
) (job.ChunkPlan, error) {
	var plan job.ChunkPlan

	err := s.run.Write(ctx, pc.Fence, func(tx Tx) error {
		j, err := claimed(ctx, tx, pc)
		if err != nil {
			return err
		}

		ns := pc.Fence.Namespace

		lookup := func(version string, seq int) (job.ChunkRow, bool, error) {
			return tx.Chunk(ctx, ns, version, seq)
		}

		var template *job.LogRef
		if newLog != nil {
			template = &newLog.Log
		}

		plan, err = job.PlanChunk(&j, stepKey(&pc, step), c, template, lookup)
		if err != nil {
			return err
		}

		if plan.CreateLog {
			if createErr := createLog(ctx, tx, &j, &logCreate{pc: &pc, step: step, log: newLog}); createErr != nil {
				return createErr
			}
		}

		if plan.Insert {
			return tx.InsertChunk(
				ctx,
				ns,
				plan.Version,
				c.Seq,
				job.ChunkRow{SHA256: c.SHA256, Size: c.Size, Confirmed: false, BlobKey: ""},
			)
		}

		return nil
	})
	if err != nil {
		return job.ChunkPlan{}, err
	}

	return plan, nil
}

// newItem names a new plan or log version and the function that makes its item
// key.
type newItem struct {
	ref  secret.ItemRef
	mk   job.NewItemKeyFunc
	kind secret.ItemKind
	id   string
}

// newItemKey creates the item key of the new item of kind and ID item, in the
// transaction tx, and returns the ID of its record.
//
// Defense in depth: the recorder builds the reference from the same item, and
// the sealer checks the namespace against the fence. This check stays so that
// a record never lands for another item than the row that commits with it.
func newItemKey(ctx context.Context, tx Tx, fence namespace.Fence, n *newItem) (secret.ItemKeyID, error) {
	switch {
	case n.mk == nil:
		return "", fmt.Errorf("%w: no item key function", job.ErrInvalid)
	case n.ref.Kind != n.kind || n.ref.Item != n.id || n.ref.Scope.Namespace != fence.Namespace:
		return "", fmt.Errorf("%w: the item key reference does not name the new %s %s", job.ErrInvalid, n.kind, n.id)
	}

	key, err := n.mk(ctx, tx.ItemKeys(fence), n.ref, fence)
	if err != nil {
		return "", fmt.Errorf("create item key of %s %s: %w", n.kind, n.id, err)
	}

	return key.ID, nil
}

// logCreate is the creation of a log version: the claim that runs the step,
// the step, and the new version.
type logCreate struct {
	pc   *job.PhaseClaim
	step string
	log  *job.NewLog
}

// createLog sets the new log version on the step record of j, stores its
// lookup row and saves j.
func createLog(ctx context.Context, tx Tx, j *job.Job, lc *logCreate) error {
	log := lc.log.Log

	key, err := newItemKey(ctx, tx, lc.pc.Fence, &newItem{
		ref: lc.log.Ref, mk: lc.log.NewKey, kind: secret.ItemKindJobLog, id: log.ID,
	})
	if err != nil {
		return err
	}

	log.ItemKey = key

	job.CreateLogRecord(j, stepKey(lc.pc, lc.step), &log)

	now, err := tx.Now(ctx)
	if err != nil {
		return err
	}

	row := LogRow{
		ID:        log.ID,
		Job:       j.ID,
		Phase:     lc.pc.Phase,
		Step:      lc.step,
		Token:     lc.pc.Claim.Token,
		CreatedMs: now.UnixMilli(),
	}
	if insertErr := tx.InsertLog(ctx, lc.pc.Fence.Namespace, row); insertErr != nil {
		return insertErr
	}

	return save(ctx, tx, j)
}

// ConfirmLogChunk implements job.RecordStore.
func (s *Store) ConfirmLogChunk(ctx context.Context, pc job.PhaseClaim, c job.ChunkConfirm) (bool, error) {
	// A sealed chunk is never empty. A confirm with no stored bytes would leave
	// a counted chunk that a backup cannot copy.
	if c.Stored.Size <= 0 {
		return false, fmt.Errorf("%w: chunk %d of log %s has no stored size", job.ErrInvalid, c.Seq, c.Version)
	}

	// Defense in depth: the recorder makes the key from the same parts, and the
	// blob view checks the namespace. This check stays so that a row never
	// names a blob that is not an attempt of its own chunk.
	if _, err := job.ChunkBlobKey(pc.Job, c.Version, c.Seq, c.BlobKey); err != nil {
		return false, err
	}

	won := false

	err := s.run.Write(ctx, pc.Fence, func(tx Tx) error {
		outcome, confirmErr := confirmChunk(ctx, tx, &pc, &c)
		won = outcome.won

		if confirmErr != nil || outcome.repeat {
			// A repeat of a confirm that committed with the same blob key
			// writes nothing and answers true at any age: the row names the
			// blob, so the caller must keep it.
			return confirmErr
		}

		// The attempt window is checked after every read and write of the
		// transaction, so a lock wait that holds the confirm past the deadline
		// is refused. The error rolls the transaction back: a late attempt
		// changes nothing.
		return checkChunkAttempt(ctx, tx, &c)
	})
	if err != nil {
		return false, err
	}

	return won, nil
}

// confirmChunk counts the chunk c in the job of pc and marks its row, with the
// blob key and the stored size and MD5, in the transaction tx. The first
// confirm of a row wins and returns true. A row that is confirmed already
// stays as it is, and the call returns true only when the row holds c.BlobKey:
// the same attempt confirmed twice. The outcome marks that repeat, which
// writes nothing.
func confirmChunk(ctx context.Context, tx Tx, pc *job.PhaseClaim, c *job.ChunkConfirm) (chunkOutcome, error) {
	j, err := claimed(ctx, tx, *pc)
	if err != nil {
		return chunkOutcome{won: false, repeat: false}, err
	}

	row, ok, err := tx.Chunk(ctx, pc.Fence.Namespace, c.Version, c.Seq)
	if err != nil {
		return chunkOutcome{won: false, repeat: false}, err
	}

	if !ok {
		return chunkOutcome{
			won:    false,
			repeat: false,
		}, fmt.Errorf(
			"%w: chunk %d of log %s was not reserved",
			job.ErrInvalid,
			c.Seq,
			c.Version,
		)
	}

	changed, err := job.ConfirmChunkRecord(&j, stepKey(pc, c.Step), c.Version, c.Seq, row)
	if err != nil {
		return chunkOutcome{won: false, repeat: false}, err
	}

	if row.Confirmed {
		same := row.BlobKey == c.BlobKey

		return chunkOutcome{won: same, repeat: same}, nil
	}

	confirmErr := tx.ConfirmChunk(
		ctx, pc.Fence.Namespace, c.Version, c.Seq, StoredChunk{Digest: c.Stored, BlobKey: c.BlobKey},
	)
	if confirmErr != nil {
		return chunkOutcome{won: false, repeat: false}, confirmErr
	}

	if changed {
		if saveErr := save(ctx, tx, &j); saveErr != nil {
			return chunkOutcome{won: false, repeat: false}, saveErr
		}
	}

	return chunkOutcome{won: true, repeat: false}, nil
}

// gatePlan loads the job of pc and applies the rules of a plan write. For a
// plan that read a state version that is not the current one, the phase fails
// in the transaction tx, with the end of its operation, and the commit keeps
// that. gatePlan then returns the job and the stale error in stale, and the
// caller returns with no further write. The caller gives the stale error to its
// own caller after the commit.
func gatePlan(ctx context.Context, tx Tx, pc *job.PhaseClaim) (planGate, error) {
	j, err := claimed(ctx, tx, *pc)
	if err != nil {
		return planGate{}, err
	}

	current, err := tx.StateVersion(ctx, pc.Fence.Namespace, j.Spec.Unit.UnitID())
	if err != nil {
		return planGate{}, err
	}

	checkErr := job.CheckPutPlan(&j, pc.Phase, current)

	switch {
	case checkErr == nil:
		return planGate{j: j, stale: nil}, nil
	case !errors.Is(checkErr, job.ErrPlanStale):
		return planGate{}, checkErr
	}

	if failErr := failPhase(ctx, tx, &j, pc, checkErr); failErr != nil {
		return planGate{}, failErr
	}

	return planGate{j: j, stale: checkErr}, nil
}

// planGate is the result of gatePlan: the job, and the stale error when the
// plan read a state version that is not the current one.
type planGate struct {
	j     job.Job
	stale error
}

// isStale reports whether the plan read a state version that is not current.
func (g *planGate) isStale() bool { return g.stale != nil }

// ReservePlan implements job.RecordStore.
func (s *Store) ReservePlan(ctx context.Context, pc job.PhaseClaim, np *job.NewPlan) (job.PlanRef, error) {
	var (
		out   job.PlanRef
		stale error
	)

	err := s.run.Write(ctx, pc.Fence, func(tx Tx) error {
		g, err := gatePlan(ctx, tx, &pc)
		if err != nil {
			return err
		}

		if g.isStale() {
			stale = g.stale

			return nil
		}

		j := g.j

		// A repeat of a reserve returns the plan that the first one recorded,
		// with no second item key. The check of the claim above ran again.
		repeat, found, err := reservedRepeat(ctx, tx, &j, &pc, np.Plan.ID)
		if err != nil {
			return err
		}

		if found {
			out = *repeat

			return nil
		}

		p := np.Plan

		key, err := newItemKey(ctx, tx, pc.Fence, &newItem{
			ref: np.Ref, mk: np.NewKey, kind: secret.ItemKindPlan, id: p.ID,
		})
		if err != nil {
			return err
		}

		p.ItemKey, p.Parts, p.StateVersion = key, nil, j.Phases[pc.Phase].StateVersion

		body, err := json.Marshal(encodeReservePlan(&p, pc))
		if err != nil {
			return fmt.Errorf("encode plan %s: %w", p.ID, err)
		}

		out = p

		return tx.InsertPlan(ctx, pc.Fence.Namespace, PlanRow{
			ID: p.ID, Job: j.ID, Body: string(body), CreatedMs: p.CreatedAt.UnixMilli(),
		})
	})
	if err != nil {
		return job.PlanRef{}, err
	}

	if stale != nil {
		return job.PlanRef{}, fmt.Errorf("reserve plan: %w", stale)
	}

	return out, nil
}

// PutPlan implements job.RecordStore.
func (s *Store) PutPlan(ctx context.Context, pc job.PhaseClaim, p job.PlanRef) (job.PlanRef, error) {
	var (
		out   job.PlanRef
		stale error
	)

	err := s.run.Write(ctx, pc.Fence, func(tx Tx) error {
		g, err := gatePlan(ctx, tx, &pc)
		if err != nil {
			return err
		}

		if g.isStale() {
			stale = g.stale

			return nil
		}

		j := g.j

		reserved, reserveErr := checkReserved(ctx, tx, &j, &pc, &p)
		if reserveErr != nil {
			return reserveErr
		}

		// Defense in depth: the recorder builds the parts and checks its
		// uploads, and the plan comes from the reserved row. This check
		// stays so that a caller of the store cannot record a plan that has
		// other parts, times or a retention than the reserve gave.
		if partsErr := job.CheckPlanParts(p.Parts); partsErr != nil {
			return partsErr
		}

		plan := *reserved
		plan.Parts, plan.StateVersion = p.Parts, j.Phases[pc.Phase].StateVersion
		j.Phases[pc.Phase].Plan = &plan

		body, err := json.Marshal(encodePlan(&plan))
		if err != nil {
			return fmt.Errorf("encode plan %s: %w", plan.ID, err)
		}

		row := PlanRow{ID: plan.ID, Job: j.ID, Body: string(body), CreatedMs: plan.CreatedAt.UnixMilli()}
		if updateErr := tx.UpdatePlan(ctx, pc.Fence.Namespace, row); updateErr != nil {
			return updateErr
		}

		out = plan

		return save(ctx, tx, &j)
	})
	if err != nil {
		return job.PlanRef{}, err
	}

	if stale != nil {
		return job.PlanRef{}, fmt.Errorf("put plan: %w", stale)
	}

	return out, nil
}

// reservedRepeat returns the reserved plan id when a reserve recorded it for
// the job j, and found true. It returns found false when no row has the ID. A
// row of another job, or a row that has parts, is job.ErrConflict: a reserve
// never takes over the plan of another job, and never reopens a plan that has
// its parts.
//
// Defense in depth: the recorder derives the ID from the claim, so a reserve
// of the plan of another job does not come from it. This check stays so that a
// caller of the store cannot reserve a plan row of another job.
func reservedRepeat(
	ctx context.Context, tx Tx, j *job.Job, pc *job.PhaseClaim, id string,
) (*job.PlanRef, bool, error) {
	row, ok, err := tx.Plan(ctx, pc.Fence.Namespace, id)
	if err != nil || !ok {
		return nil, false, err
	}

	if row.Job != j.ID {
		return nil, false, fmt.Errorf("%w: plan %s belongs to another job", job.ErrConflict, id)
	}

	var body planBody
	if err = json.Unmarshal([]byte(row.Body), &body); err != nil {
		return nil, false, fmt.Errorf("%w: plan %s: %w", ErrDamaged, id, err)
	}

	reserved := decodePlan(&body)

	switch {
	case reserved == nil:
		return nil, false, fmt.Errorf("%w: plan %s: no plan in the row", ErrDamaged, id)
	case len(reserved.Parts) > 0:
		return nil, false, fmt.Errorf("%w: plan %s has parts", job.ErrConflict, id)
	}

	return reserved, true, nil
}

// checkReserved checks that p is the plan that a reserve recorded for the job
// j under the phase and the claim of pc: the row exists, belongs to j, has no
// part, names the same item key, and its reserve names the phase, the
// operation and the token of pc. It returns the plan of the row.
func checkReserved(ctx context.Context, tx Tx, j *job.Job, pc *job.PhaseClaim, p *job.PlanRef) (*job.PlanRef, error) {
	row, ok, err := tx.Plan(ctx, pc.Fence.Namespace, p.ID)
	if err != nil {
		return nil, err
	}

	if !ok || row.Job != j.ID {
		return nil, fmt.Errorf("%w: plan %s was not reserved for job %s", job.ErrNotFound, p.ID, j.ID)
	}

	var body planBody
	if err = json.Unmarshal([]byte(row.Body), &body); err != nil {
		return nil, fmt.Errorf("%w: plan %s: %w", ErrDamaged, p.ID, err)
	}

	reserved := decodePlan(&body)

	switch {
	case reserved == nil:
		return nil, fmt.Errorf("%w: plan %s: no plan in the row", ErrDamaged, p.ID)
	case len(reserved.Parts) > 0:
		return nil, fmt.Errorf("%w: plan %s has parts", job.ErrConflict, p.ID)
	case reserved.ItemKey != p.ItemKey:
		return nil, fmt.Errorf("%w: plan %s has another item key than its reserve", job.ErrInvalid, p.ID)
	case body.ReservePhase != pc.Phase || body.ReserveOperation != string(pc.Claim.Operation) ||
		body.ReserveToken != pc.Claim.Token:
		// Defense in depth: the write checks the live claim of pc before this
		// read. This check stays so that a later claim of the same job cannot
		// fill the reserve that another claim made, and a row without the
		// binding fields is refused.
		return nil, fmt.Errorf("%w: plan %s was reserved by another phase or claim", job.ErrConflict, p.ID)
	}

	return reserved, nil
}

// failPhase fails the current phase of j and ends the operation of the claim
// of pc in the transaction tx, with the reason why.
func failPhase(ctx context.Context, tx Tx, j *job.Job, pc *job.PhaseClaim, why error) error {
	msg := why.Error()

	c := job.Change{
		Job: j.ID, Version: j.Version, Event: job.EventPhaseFailed, Wait: job.WaitNone, Reason: msg, Dispatch: nil,
		PlanNotBefore: time.Time{}, Executor: "", Claim: nil, Delivery: nil, MergedAt: time.Time{}, Approval: nil,
		End: &job.OperationEnd{
			Claim: pc.Claim, Kind: job.EndFail, Failure: durable.Failure{Message: msg, Final: true, RetryAfter: 0},
		},
	}

	if _, err := applyIn(ctx, tx, &c, pc.Fence.Namespace); err != nil {
		return fmt.Errorf("fail the phase of a stale plan: %w", err)
	}

	return nil
}

// Delivered implements job.RecordStore.
func (s *Store) Delivered(ctx context.Context, pc job.PhaseClaim, d job.DeliveryResult, e audit.Entry) error {
	entries := []audit.Entry{e}

	if err := checkEntries(entries, pc.Fence); err != nil {
		return err
	}

	return s.run.Write(ctx, pc.Fence, func(tx Tx) error {
		j, err := claimed(ctx, tx, pc)
		if err != nil {
			return err
		}

		changed, err := job.CheckDelivered(&j, pc.Phase, &d)
		if err != nil || !changed {
			return err
		}

		j.Delivery = &d

		if saveErr := save(ctx, tx, &j); saveErr != nil {
			return saveErr
		}

		return tx.AppendAudit(ctx, entries)
	})
}

// Plan implements job.RecordStore.
func (s *Store) Plan(ctx context.Context, ns namespace.Name, id string) (job.ArtifactRecord, error) {
	var out job.ArtifactRecord

	err := s.run.Read(ctx, func(tx Tx) error {
		row, ok, err := tx.Plan(ctx, ns, id)
		if err != nil {
			return err
		}

		if !ok {
			return job.ErrNotFound
		}

		j, err := loadJob(ctx, tx, row.Job, ns)
		if err != nil {
			return err
		}

		var body planBody
		if err = json.Unmarshal([]byte(row.Body), &body); err != nil {
			return fmt.Errorf("%w: plan %s: %w", ErrDamaged, id, err)
		}

		plan := decodePlan(&body)
		if plan == nil || len(plan.Parts) == 0 {
			// A plan with no part is a reserve: its parts are not stored yet.
			return job.ErrNotFound
		}

		out = job.ArtifactRecord{Job: j.ID, Unit: j.Spec.Unit, Step: "", Plan: plan, Log: nil, ChunkKeys: nil}

		return nil
	})
	if err != nil {
		return job.ArtifactRecord{}, err
	}

	return out, nil
}

// Log implements job.RecordStore.
func (s *Store) Log(ctx context.Context, ns namespace.Name, version string) (job.ArtifactRecord, error) {
	var out job.ArtifactRecord

	err := s.run.Read(ctx, func(tx Tx) error {
		row, ok, err := tx.Log(ctx, ns, version)
		if err != nil {
			return err
		}

		if !ok {
			return job.ErrNotFound
		}

		j, err := loadJob(ctx, tx, row.Job, ns)
		if err != nil {
			return err
		}

		log := logOfRow(&j, &row)
		if log == nil {
			return fmt.Errorf("%w: log %s is not in the step record of job %s", ErrDamaged, version, j.ID)
		}

		keys, keysErr := chunkKeysOf(ctx, tx, ns, version)
		if keysErr != nil {
			return keysErr
		}

		out = job.ArtifactRecord{Job: j.ID, Unit: j.Spec.Unit, Step: row.Step, Plan: nil, Log: log, ChunkKeys: keys}

		return nil
	})
	if err != nil {
		return job.ArtifactRecord{}, err
	}

	return out, nil
}

// chunkKeysOf returns the blob key of each confirmed chunk of the log version,
// by chunk number. It returns ErrDamaged for rows whose numbers are not 0, 1,
// 2 and so on.
func chunkKeysOf(ctx context.Context, tx Tx, ns namespace.Name, version string) ([]string, error) {
	rows, err := tx.ChunkKeys(ctx, ns, version)
	if err != nil {
		return nil, err
	}

	keys := make([]string, 0, len(rows))

	for i, r := range rows {
		if r.Seq != i {
			return nil, fmt.Errorf("%w: log %s holds chunk %d where chunk %d is next", ErrDamaged, version, r.Seq, i)
		}

		keys = append(keys, r.BlobKey)
	}

	return keys, nil
}

// logOfRow returns the log version of row in the step record of j, or nil.
func logOfRow(j *job.Job, row *LogRow) *job.LogRef {
	if row.Phase < 0 || row.Phase >= len(j.Phases) {
		return nil
	}

	for i := range j.Phases[row.Phase].Steps {
		r := &j.Phases[row.Phase].Steps[i]
		if r.Name == row.Step && r.Token == row.Token && r.Log != nil && r.Log.ID == row.ID {
			return r.Log
		}
	}

	return nil
}

// ChunkRowCheck is a stored chunk row that CheckChunkRow checks: the chunk
// Seq of the log Log of the job Job.
type ChunkRowCheck struct {
	Job, Log  string
	Seq, Size int64
	SHA256    string
	// Confirmed is true when the chunk counts in its log. BlobKey is the key of
	// the blob of the attempt that won it.
	Confirmed bool
	BlobKey   string
}

// CheckChunkRow returns an error that wraps ErrDamaged unless a stored chunk
// row has the rules of a reserve: a number below job.MaxLogChunks, a size from
// 0 to job.MaxLogChunk and a SHA-256. A chunk that is confirmed has the blob
// key of an attempt of this chunk of this log (job.ChunkBlobKey), and a chunk
// that is not has none. A restore uses it on the rows of a staged backup.
func CheckChunkRow(r *ChunkRowCheck) error {
	if r.Seq < 0 || r.Seq >= job.MaxLogChunks || r.Size < 0 || r.Size > job.MaxLogChunk || !job.ValidHash(r.SHA256) {
		return fmt.Errorf("%w: chunk %d of log %s is not valid", ErrDamaged, r.Seq, r.Log)
	}

	if !r.Confirmed {
		if r.BlobKey != "" {
			return fmt.Errorf("%w: chunk %d of log %s is not confirmed and has a blob key", ErrDamaged, r.Seq, r.Log)
		}

		return nil
	}

	if _, err := job.ChunkBlobKey(job.ID(r.Job), r.Log, int(r.Seq), r.BlobKey); err != nil {
		return fmt.Errorf(
			"%w: chunk %d of log %s has a blob key that is not its own: %w",
			ErrDamaged,
			r.Seq,
			r.Log,
			err,
		)
	}

	return nil
}

// ParsePlan returns the plan that the JSON body of the row with the ID id
// holds. It returns an error that wraps ErrDamaged for a body that is not JSON
// and for a plan with another ID. It does not check the parts.
func ParsePlan(id, body string) (*job.PlanRef, error) {
	var b planBody
	if err := json.Unmarshal([]byte(body), &b); err != nil {
		return nil, fmt.Errorf("%w: plan %s: %w", ErrDamaged, id, err)
	}

	p := decodePlan(&b)
	if p == nil || p.ID != id {
		return nil, fmt.Errorf("%w: plan %s does not match its row", ErrDamaged, id)
	}

	return p, nil
}

// CheckPlanBody returns an error that wraps ErrDamaged unless body is the JSON
// of a plan with the ID id and valid parts. A restore uses it on the rows of
// a staged backup, so that a row that the server would not have written never
// goes live.
func CheckPlanBody(id, body string) error {
	p, err := ParsePlan(id, body)
	if err != nil {
		return err
	}

	// A reserved plan has no part yet. A filled plan has the parts that PutPlan
	// accepts, with the same rules: no kind twice and no part above the size
	// limit.
	if len(p.Parts) == 0 {
		return nil
	}

	if err := job.CheckPlanParts(p.Parts); err != nil {
		return fmt.Errorf("%w: plan %s has parts that are not valid: %w", ErrDamaged, id, err)
	}

	return nil
}

// chunkOutcome is the answer of confirmChunk: won is the answer of the
// confirm, and repeat is true for a repeat of a confirm that the row holds
// with the same blob key.
type chunkOutcome struct {
	won    bool
	repeat bool
}

// checkChunkAttempt returns blob.ErrAttemptExpired when the attempt of c is
// outside its time window by the clock of the database.
func checkChunkAttempt(ctx context.Context, tx Tx, c *job.ChunkConfirm) error {
	now, err := tx.Now(ctx)
	if err != nil {
		return err
	}

	if attemptErr := blob.CheckAttempt(c.AttemptStartedAt, now); attemptErr != nil {
		return fmt.Errorf("confirm chunk %d of log %s: %w", c.Seq, c.Version, attemptErr)
	}

	return nil
}

var _ job.ArtifactKeyStore = (*Store)(nil)

// ArtifactNames implements job.ArtifactKeyStore. It reads the job first, so an
// unknown job and a job of another namespace are job.ErrNotFound, never an
// empty set.
func (s *Store) ArtifactNames(ctx context.Context, ns namespace.Name, id job.ID) (job.ArtifactNames, error) {
	var out job.ArtifactNames

	err := s.run.Read(ctx, func(tx Tx) error {
		if _, err := loadJob(ctx, tx, id, ns); err != nil {
			return err
		}

		plans, err := tx.JobPlanIDs(ctx, ns, id)
		if err != nil {
			return err
		}

		chunks, err := tx.JobChunkKeys(ctx, ns, id)
		if err != nil {
			return err
		}

		parts, err := storedPartKinds(ctx, tx, ns, plans)
		if err != nil {
			return err
		}

		out = job.ArtifactNames{Plans: plans, Parts: parts, ChunkKeys: chunks}

		return nil
	})
	if err != nil {
		return job.ArtifactNames{}, err
	}

	return out, nil
}

// storedPartKinds returns, for each plan of ids that has parts, the kinds of its
// parts. A reserved plan has no entry. A row that does not decode is
// ErrDamaged, so the sweep deletes nothing for a job whose plan it cannot read.
func storedPartKinds(ctx context.Context, tx Tx, ns namespace.Name, ids []string) (map[string][]job.OutputKind, error) {
	out := make(map[string][]job.OutputKind, len(ids))

	for _, id := range ids {
		row, ok, err := tx.Plan(ctx, ns, id)
		if err != nil {
			return nil, err
		}

		if !ok {
			continue
		}

		var body planBody
		if err = json.Unmarshal([]byte(row.Body), &body); err != nil {
			return nil, fmt.Errorf("%w: plan %s: %w", ErrDamaged, id, err)
		}

		plan := decodePlan(&body)
		if plan == nil {
			return nil, fmt.Errorf("%w: plan %s: no plan in the row", ErrDamaged, id)
		}

		for i := range plan.Parts {
			out[id] = append(out[id], plan.Parts[i].Kind)
		}
	}

	return out, nil
}

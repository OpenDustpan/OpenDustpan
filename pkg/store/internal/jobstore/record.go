package jobstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
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
// version, at the time of the database.
func save(ctx context.Context, tx Tx, j *job.Job) error {
	now, err := tx.Now(ctx)
	if err != nil {
		return err
	}

	prev := j.Version
	j.Version, j.UpdatedAt = prev+1, now

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
				job.ChunkRow{SHA256: c.SHA256, Size: c.Size, Confirmed: false},
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
func (s *Store) ConfirmLogChunk(ctx context.Context, pc job.PhaseClaim, step, version string, seq int) error {
	return s.run.Write(ctx, pc.Fence, func(tx Tx) error {
		j, err := claimed(ctx, tx, pc)
		if err != nil {
			return err
		}

		ns := pc.Fence.Namespace

		row, ok, err := tx.Chunk(ctx, ns, version, seq)
		if err != nil {
			return err
		}

		if !ok {
			return fmt.Errorf("%w: chunk %d of log %s was not reserved", job.ErrInvalid, seq, version)
		}

		changed, err := job.ConfirmChunkRecord(&j, stepKey(&pc, step), version, seq, row)
		if err != nil {
			return err
		}

		if !row.Confirmed {
			if confirmErr := tx.ConfirmChunk(ctx, ns, version, seq); confirmErr != nil {
				return confirmErr
			}
		}

		if !changed {
			return nil
		}

		return save(ctx, tx, &j)
	})
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

		p := np.Plan

		key, err := newItemKey(ctx, tx, pc.Fence, &newItem{
			ref: np.Ref, mk: np.NewKey, kind: secret.ItemKindPlan, id: p.ID,
		})
		if err != nil {
			return err
		}

		p.ItemKey, p.Parts, p.StateVersion = key, nil, j.Phases[pc.Phase].StateVersion

		body, err := json.Marshal(encodePlan(&p))
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

		if reserveErr := checkReserved(ctx, tx, &j, &pc, &p); reserveErr != nil {
			return reserveErr
		}

		p.StateVersion = j.Phases[pc.Phase].StateVersion
		j.Phases[pc.Phase].Plan = &p

		body, err := json.Marshal(encodePlan(&p))
		if err != nil {
			return fmt.Errorf("encode plan %s: %w", p.ID, err)
		}

		row := PlanRow{ID: p.ID, Job: j.ID, Body: string(body), CreatedMs: p.CreatedAt.UnixMilli()}
		if updateErr := tx.UpdatePlan(ctx, pc.Fence.Namespace, row); updateErr != nil {
			return updateErr
		}

		out = p

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

// checkReserved checks that p is the plan that a reserve recorded for the job
// j: the row exists, belongs to j, has no part, and names the same item key.
func checkReserved(ctx context.Context, tx Tx, j *job.Job, pc *job.PhaseClaim, p *job.PlanRef) error {
	row, ok, err := tx.Plan(ctx, pc.Fence.Namespace, p.ID)
	if err != nil {
		return err
	}

	if !ok || row.Job != j.ID {
		return fmt.Errorf("%w: plan %s was not reserved for job %s", job.ErrNotFound, p.ID, j.ID)
	}

	var body planBody
	if err = json.Unmarshal([]byte(row.Body), &body); err != nil {
		return fmt.Errorf("%w: plan %s: %w", ErrDamaged, p.ID, err)
	}

	switch reserved := decodePlan(&body); {
	case reserved == nil:
		return fmt.Errorf("%w: plan %s: no plan in the row", ErrDamaged, p.ID)
	case len(reserved.Parts) > 0:
		return fmt.Errorf("%w: plan %s has parts", job.ErrConflict, p.ID)
	case reserved.ItemKey != p.ItemKey:
		return fmt.Errorf("%w: plan %s has another item key than its reserve", job.ErrInvalid, p.ID)
	}

	return nil
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

		out = job.ArtifactRecord{Job: j.ID, Unit: j.Spec.Unit, Step: "", Plan: plan, Log: nil}

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

		out = job.ArtifactRecord{Job: j.ID, Unit: j.Spec.Unit, Step: row.Step, Plan: nil, Log: log}

		return nil
	})
	if err != nil {
		return job.ArtifactRecord{}, err
	}

	return out, nil
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

// CheckPlanBody returns an error that wraps ErrDamaged unless body is the JSON
// of a plan with the ID id and valid parts. A restore uses it on the rows of
// a staged backup, so that a row that the server would not have written never
// goes live.
func CheckPlanBody(id, body string) error {
	var b planBody
	if err := json.Unmarshal([]byte(body), &b); err != nil {
		return fmt.Errorf("%w: plan %s: %w", ErrDamaged, id, err)
	}

	p := decodePlan(&b)
	if p == nil || p.ID != id || len(p.Parts) > 2 {
		return fmt.Errorf("%w: plan %s does not match its row", ErrDamaged, id)
	}

	for i := range p.Parts {
		part := &p.Parts[i]
		if (part.Kind != job.OutputPlanDisplay && part.Kind != job.OutputPlanApply) || part.Size < 0 ||
			!job.ValidHash(part.SHA256) {
			return fmt.Errorf("%w: plan %s has a part that is not valid", ErrDamaged, id)
		}
	}

	return nil
}

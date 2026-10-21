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
	ctx context.Context, pc job.PhaseClaim, step string, c job.ChunkReserve, newLog *job.LogRef,
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

		plan, err = job.PlanChunk(&j, stepKey(&pc, step), c, newLog, lookup)
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

// logCreate is the creation of a log version: the claim that runs the step,
// the step, and the new version.
type logCreate struct {
	pc   *job.PhaseClaim
	step string
	log  *job.LogRef
}

// createLog sets the new log version on the step record of j, stores its
// lookup row and saves j.
func createLog(ctx context.Context, tx Tx, j *job.Job, lc *logCreate) error {
	job.CreateLogRecord(j, stepKey(lc.pc, lc.step), lc.log)

	now, err := tx.Now(ctx)
	if err != nil {
		return err
	}

	row := LogRow{
		ID:        lc.log.ID,
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

// PutPlan implements job.RecordStore.
func (s *Store) PutPlan(ctx context.Context, pc job.PhaseClaim, p job.PlanRef) (job.PlanRef, error) {
	var (
		out   job.PlanRef
		stale error
	)

	err := s.run.Write(ctx, pc.Fence, func(tx Tx) error {
		j, err := claimed(ctx, tx, pc)
		if err != nil {
			return err
		}

		current, err := tx.StateVersion(ctx, pc.Fence.Namespace, j.Spec.Unit.UnitID())
		if err != nil {
			return err
		}

		if checkErr := job.CheckPutPlan(&j, pc.Phase, current); checkErr != nil {
			if !errors.Is(checkErr, job.ErrPlanStale) {
				return checkErr
			}

			// The plan read a state that is not the current one. The phase fails
			// in this transaction, with the end of its operation, and the commit
			// keeps that. The caller gets ErrPlanStale after the commit.
			stale = checkErr

			return failPhase(ctx, tx, &j, &pc, checkErr)
		}

		p.StateVersion = j.Phases[pc.Phase].StateVersion
		j.Phases[pc.Phase].Plan = &p

		body, err := json.Marshal(encodePlan(&p))
		if err != nil {
			return fmt.Errorf("encode plan %s: %w", p.ID, err)
		}

		row := PlanRow{ID: p.ID, Job: j.ID, Body: string(body), CreatedMs: p.CreatedAt.UnixMilli()}
		if insertErr := tx.InsertPlan(ctx, pc.Fence.Namespace, row); insertErr != nil {
			return insertErr
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

		out = job.ArtifactRecord{Job: j.ID, Unit: j.Spec.Unit, Step: "", Plan: decodePlan(&body), Log: nil}

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
	if p == nil || p.ID != id || len(p.Parts) == 0 || len(p.Parts) > 2 {
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

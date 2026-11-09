package jobstore

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Apply implements job.Store.
func (s *Store) Apply(ctx context.Context, c job.Change, fence namespace.Fence, e []audit.Entry) (job.Job, error) {
	if err := checkEntries(e, fence); err != nil {
		return job.Job{}, err
	}

	if err := checkEnd(&c); err != nil {
		return job.Job{}, err
	}

	if err := c.ValidateClaim(); err != nil {
		return job.Job{}, err
	}

	var out job.Job

	err := s.run.Write(ctx, fence, func(tx Tx) error {
		next, err := applyIn(ctx, tx, &c, fence.Namespace)
		if err != nil {
			return err
		}

		if err := tx.AppendAudit(ctx, e); err != nil {
			return err
		}

		out = next

		return nil
	})

	return out, err
}

// applyIn makes the change c in the transaction tx, and returns the job after
// it. The job must belong to the namespace ns.
func applyIn(ctx context.Context, tx Tx, c *job.Change, ns namespace.Name) (job.Job, error) {
	cur, err := loadJob(ctx, tx, c.Job, ns)
	if err != nil {
		return job.Job{}, err
	}

	if cur.Version != c.Version {
		return job.Job{}, job.ErrConflict
	}

	to, err := job.Next(cur.State, c.Event)
	if err != nil {
		return job.Job{}, err
	}

	now, err := tx.Now(ctx)
	if err != nil {
		return job.Job{}, err
	}

	// The write moves the update time of the row even when the clock of the
	// database did not move since the last write. The clock time stays the
	// time of the phase records, of the state change and of the freeze
	// check: a reader compares those with the clock.
	updated := nextUpdateAt(now, cur.UpdatedAt)

	next := cloneJob(&cur)

	if err := change(ctx, tx, &next, c, now); err != nil {
		return job.Job{}, err
	}

	settle(&next, to, c, cur.Version, updated)

	if err := closeLate(ctx, tx, &next, c.Event); err != nil {
		return job.Job{}, err
	}

	if err := endOperation(ctx, tx, &cur, c); err != nil {
		return job.Job{}, err
	}

	if err := bindClaim(ctx, tx, &cur, c); err != nil {
		return job.Job{}, err
	}

	if err := afterChange(ctx, tx, &cur, &next, c.Event); err != nil {
		return job.Job{}, err
	}

	if next.State != cur.State {
		next.StateChangedAt = now
	}

	if err := update(ctx, tx, &next, cur.Version); err != nil {
		return job.Job{}, err
	}

	return next, nil
}

// endOperation ends the durable operation of c.End, in the transaction tx of
// the change of cur. It does nothing when c has no end. The end binds the
// event to its claim: the job must be at the phase whose operation the claim
// names, and the durable store checks that the claim is the live claim of
// that operation. A failed end rolls the whole change back.
func endOperation(ctx context.Context, tx Tx, cur *job.Job, c *job.Change) error {
	if c.End == nil {
		return nil
	}

	// Defense in depth: Apply checks the end before it opens the
	// transaction, and this check stays so that a new caller of applyIn cannot
	// end an operation with an event that carries no phase.
	if err := checkEnd(c); err != nil {
		return err
	}

	if cur.Current >= len(cur.Phases) || cur.Phases[cur.Current].Operation != string(c.End.Claim.Operation) {
		return fmt.Errorf("%w: %w: job %s is not at the phase of operation %s", job.ErrClaimLost,
			durable.ErrClaimLost, cur.ID, c.End.Claim.Operation)
	}

	err := tx.EndOperation(ctx, c.End)
	if c.End.Kind == job.EndComplete && errors.Is(err, durable.ErrCanceled) {
		// A cancel arrived while the phase ran. The phase did end, so the
		// event of the job says so, and the release ends the operation
		// canceled.
		release := *c.End
		release.Kind = job.EndRelease
		err = tx.EndOperation(ctx, &release)
	}

	if errors.Is(err, durable.ErrClaimLost) {
		return fmt.Errorf("end operation %s: %w: %w", c.End.Claim.Operation, job.ErrClaimLost, err)
	}

	if err != nil {
		return fmt.Errorf("end operation %s: %w", c.End.Claim.Operation, err)
	}

	return nil
}

// bindClaim binds the event of c.Claim to its claim, in the transaction tx of
// the change of cur. It does nothing when c has no claim. The job must be at
// the phase whose operation the claim names, and the durable store must hold
// the claim as the live claim of that operation. A failed check rolls the
// whole change back, so no executor, claim count or audit entry of a claim
// that was lost is stored.
func bindClaim(ctx context.Context, tx Tx, cur *job.Job, c *job.Change) error {
	if c.Claim == nil {
		return nil
	}

	// Defense in depth: Apply checks the claim before it opens the
	// transaction, and this check stays so that a new caller of applyIn
	// cannot bind an event to a claim that names nothing.
	if err := c.ValidateClaim(); err != nil {
		return err
	}

	// Defense in depth: the claimer reads the job, checks that its current
	// phase names the operation of the claim, and sends the version that it
	// read, and applyIn refuses another version with job.ErrConflict before
	// this point. So the claimer never reaches this check. It stays so that
	// a new caller of Apply cannot bind a claim to a job at another phase.
	if cur.Current >= len(cur.Phases) || cur.Phases[cur.Current].Operation != string(c.Claim.Operation) {
		return fmt.Errorf("%w: %w: job %s is not at the phase of operation %s", job.ErrClaimLost,
			durable.ErrClaimLost, cur.ID, c.Claim.Operation)
	}

	err := tx.CheckClaim(ctx, *c.Claim)
	if errors.Is(err, durable.ErrClaimLost) {
		return fmt.Errorf("claim of operation %s: %w: %w", c.Claim.Operation, job.ErrClaimLost, err)
	}

	if err != nil {
		return fmt.Errorf("check claim of operation %s: %w", c.Claim.Operation, err)
	}

	return nil
}

// checkEnd returns job.ErrInvalid for a change whose end is not valid or
// whose event cannot carry an end.
func checkEnd(c *job.Change) error {
	if c.End == nil {
		return nil
	}

	if !job.AllowsEnd(c.Event) {
		return fmt.Errorf("%w: event %s cannot end an operation", job.ErrInvalid, c.Event)
	}

	return c.End.Validate()
}

// loadJob returns the job id of the namespace ns, or job.ErrNotFound.
//
// Defense in depth: the fence names one namespace, and a job of another
// namespace is unknown to it, whatever its ID.
func loadJob(ctx context.Context, tx Tx, id job.ID, ns namespace.Name) (job.Job, error) {
	row, ok, err := tx.Job(ctx, id)
	if err != nil {
		return job.Job{}, err
	}

	if !ok || row.Namespace != ns {
		return job.Job{}, job.ErrNotFound
	}

	return jobOf(&row)
}

// settle sets the state, the version, the update time, the wait reason and
// the reason of next after the event of c moved the job from the version prev
// to state to. updated is the update time of the write.
func settle(next *job.Job, to job.State, c *job.Change, prev int64, updated time.Time) {
	next.State, next.Version, next.UpdatedAt = to, prev+1, updated

	next.Wait = job.WaitNone
	if to == job.StateQueued {
		next.Wait = c.Wait
	}

	// A terminal state records why the job ended, and a replan records why the
	// job plans again while it stays queued.
	if to.Terminal() || c.Event == job.EventReplan {
		next.Reason = c.Reason
	}
}

// ObserveStateVersion implements job.Store.
func (s *Store) ObserveStateVersion(ctx context.Context, v job.StateVersion, fence namespace.Fence) ([]job.Job,
	error,
) {
	if !v.Unit.IsUnit() || v.Unit.UnitID() == "" || v.Unit.Namespace() != fence.Namespace {
		return nil, fmt.Errorf("%w: a state version names a unit of the namespace of the fence", job.ErrInvalid)
	}

	var closed []job.Job

	err := s.run.Write(ctx, fence, func(tx Tx) error {
		var err error

		closed, err = observeIn(ctx, tx, &v, fence.Namespace, s.logger())

		return err
	})
	if err != nil {
		return nil, err
	}

	return closed, nil
}

// CloseAwaiting closes the jobs of the unit v.UnitID of the namespace ns that
// wait for a state version, when the final version v closes them, in the
// transaction tx, and returns them. A store that publishes a final state
// version in the transaction of its own write calls it there, so the version
// and the close of the jobs that wait for it commit together. The caller
// records v as the last final version of the unit too.
//
// The closing record names the unit as the job itself names it: the caller
// names the unit by its ID only. A nil logger discards.
func CloseAwaiting(
	ctx context.Context, tx CloseTx, ns namespace.Name, v FinalVersion, log *slog.Logger,
) ([]job.Job, error) {
	rows, err := tx.Awaiting(ctx, ns, v.UnitID)
	if err != nil {
		return nil, err
	}

	return closeRun{tx: tx, ns: ns, log: log}.closeAwaiting(ctx, rows, closeVersion{
		VersionID: v.VersionID, Commit: v.Commit, Job: v.Job, At: v.At,
	})
}

// closeVersion is the final state version that closes the jobs that wait for
// one: its version ID, the job and the commit that its writer reported, and
// its time.
type closeVersion struct {
	VersionID string
	Commit    string
	Job       job.ID
	At        time.Time
}

// observeIn closes the jobs that the final state version v closes, and
// records v as the last final version of its unit. It does nothing for a
// version that is not final.
func observeIn(
	ctx context.Context, tx Tx, v *job.StateVersion, ns namespace.Name, log *slog.Logger,
) ([]job.Job, error) {
	if !v.Final {
		return nil, nil
	}

	final := FinalVersion{
		UnitID: v.Unit.UnitID(), VersionID: v.VersionID, Commit: v.Commit, Job: v.Job, At: v.At,
	}

	closed, err := CloseAwaiting(ctx, tx, ns, final, log)
	if err != nil {
		return nil, err
	}

	return closed, tx.PutFinalVersion(ctx, ns, final)
}

// closeRun carries what the close of a state version reads with: the
// transaction, the namespace of the version, and the logger that records a
// damaged row.
type closeRun struct {
	tx  CloseTx
	ns  namespace.Name
	log *slog.Logger
}

// closeAwaiting closes each job of rows that the final version v closes, and
// returns them. It refuses a row of another namespace than the run, and it
// skips a row that does not decode: a damaged job is never closed, and the
// log records it once. The closing record names the unit as the job itself
// names it, so the record stays valid after a rename.
func (c closeRun) closeAwaiting(ctx context.Context, rows []Row, v closeVersion) ([]job.Job, error) {
	// The clock is read only when a job waits: a write that publishes a final
	// version and closes no job pays no extra query.
	if len(rows) == 0 {
		return nil, nil
	}

	now, nowErr := c.tx.Now(ctx)
	if nowErr != nil {
		return nil, nowErr
	}

	var closed []job.Job

	for i := range rows {
		j, done, err := c.closeRow(ctx, &rows[i], v, now)
		if err != nil {
			return nil, err
		}

		if !done {
			continue
		}

		closed = append(closed, j)
	}

	return closed, nil
}

// closeRow closes the job of the row r that the final version v closes, and
// returns it with true. It returns false for a row that does not wait for v,
// and for a row that does not decode: a damaged job is never closed, and the
// log records it once.
func (c closeRun) closeRow(ctx context.Context, r *Row, v closeVersion, now time.Time) (job.Job, bool, error) {
	var none job.Job

	// Defense in depth: the query of Awaiting filters by the namespace too;
	// this check stays so that a Tx that skips that filter cannot close the
	// job of another namespace.
	if r.Namespace != c.ns {
		return none, false, job.ErrNotFound
	}

	j, err := jobOf(r)
	if err != nil {
		if !errors.Is(err, job.ErrDamaged) {
			return none, false, err
		}

		// A damaged row stays where it is: the close loop cannot read
		// it, so it never closes it. A person reads the reason and
		// cancels the job by its ID.
		c.logger().WarnContext(ctx, "close of a state version skipped a damaged job row",
			slog.String("job", string(r.ID)), slog.String("reason", DamageReason(err)))

		return none, false, nil
	}

	closing := job.StateVersion{
		Unit: j.Spec.Unit, VersionID: v.VersionID, Commit: v.Commit, Job: v.Job, At: v.At, Final: true,
	}

	if !j.ClosedBy(closing) {
		return none, false, nil
	}

	if err := closeJob(ctx, c.tx, &j, closing); err != nil {
		return none, false, err
	}

	at := nextUpdateAt(now, j.UpdatedAt)

	prev := j.Version
	j.Version++
	// The update time moves; the state change stays on the clock.
	j.UpdatedAt, j.StateChangedAt = at, now

	if err := update(ctx, c.tx, &j, prev); err != nil {
		return none, false, err
	}

	return j, true, nil
}

// logger returns the logger of the run, or a logger that discards.
func (c closeRun) logger() *slog.Logger {
	if c.log != nil {
		return c.log
	}

	return slog.New(slog.DiscardHandler)
}

// SetVerification implements job.Store.
func (s *Store) SetVerification(ctx context.Context, id job.ID, v job.Verification, fence namespace.Fence,
	e []audit.Entry,
) error {
	if err := checkEntries(e, fence); err != nil {
		return err
	}

	return s.run.Write(ctx, fence, func(tx Tx) error {
		changed, err := setVerificationIn(ctx, tx, id, v, fence.Namespace)
		if err != nil || !changed {
			return err
		}

		return tx.AppendAudit(ctx, e)
	})
}

// setVerificationIn records v as the verification of the closed job id, and
// reports whether it changed the job.
func setVerificationIn(
	ctx context.Context, tx Tx, id job.ID, v job.Verification, ns namespace.Name,
) (bool, error) {
	j, err := loadJob(ctx, tx, id, ns)
	if err != nil {
		return false, err
	}

	if j.Closed == nil || (v != job.VerificationVerified && v != job.VerificationUnverified) {
		return false, fmt.Errorf("%w: verification %q of job %s", job.ErrInvalid, v, id)
	}

	if j.Verification == v {
		return false, nil
	}

	now, err := tx.Now(ctx)
	if err != nil {
		return false, err
	}

	now = nextUpdateAt(now, j.UpdatedAt)

	prev := j.Version
	j.Verification, j.Version, j.UpdatedAt = v, prev+1, now

	return true, update(ctx, tx, &j, prev)
}

// AppendStepRecord appends r to the step records of the phase of pc, and
// raises the version of the job. It is the write of the recorder of a running
// phase, and it checks the claim like every write of the executor: in the
// transaction of the write, the namespace fence of pc is current, the job is
// running or canceling at the phase of pc with the operation of the claim of
// pc, the claim is the live claim of that operation on the clock of the
// database, and r carries the token of that claim. It returns
// job.ErrNotFound for an unknown job, job.ErrClaimLost (with
// durable.ErrClaimLost) for a claim that is not live or a phase that is not
// the current phase of the job with that operation, job.ErrInvalid for a record
// with another token, and namespace.ErrFenced for a stale fence. It does not
// refuse a requested cancel: the recorder refuses the begin of a step during
// a cancel, and still accepts the record of how a phase stopped.
func (s *Store) AppendStepRecord(ctx context.Context, pc job.PhaseClaim, r job.StepRecord) error {
	if r.Token != pc.Claim.Token {
		return fmt.Errorf("%w: the record of step %q carries token %d, the claim has token %d", job.ErrInvalid, r.Name,
			r.Token, pc.Claim.Token)
	}

	return s.run.Write(ctx, pc.Fence, func(tx Tx) error {
		j, err := loadJob(ctx, tx, pc.Job, pc.Fence.Namespace)
		if err != nil {
			return err
		}

		if !runsPhase(&j, pc) {
			return fmt.Errorf("%w: %w: job %s is %s at phase %d", job.ErrClaimLost, durable.ErrClaimLost, j.ID, j.State,
				j.Current)
		}

		if claimErr := tx.CheckClaim(ctx, pc.Claim); claimErr != nil {
			return fmt.Errorf("claim of job %s: %w: %w", j.ID, job.ErrClaimLost, claimErr)
		}

		now, err := tx.Now(ctx)
		if err != nil {
			return err
		}

		now = nextUpdateAt(now, j.UpdatedAt)

		prev := j.Version
		j.Phases[pc.Phase].Steps = append(j.Phases[pc.Phase].Steps, r)
		j.Version, j.UpdatedAt = prev+1, now

		return update(ctx, tx, &j, prev)
	})
}

// runsPhase reports whether j is running or canceling at the phase of pc,
// with the operation of the claim of pc.
func runsPhase(j *job.Job, pc job.PhaseClaim) bool {
	return (j.State == job.StateRunning || j.State == job.StateCanceling) && j.Current == pc.Phase &&
		pc.Phase >= 0 && pc.Phase < len(j.Phases) && j.Phases[pc.Phase].Operation == string(pc.Claim.Operation)
}

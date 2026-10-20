package jobstore

import (
	"context"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Apply implements job.Store.
func (s *Store) Apply(ctx context.Context, c job.Change, fence namespace.Fence, e []audit.Entry) (job.Job, error) {
	if err := checkEntries(e, fence); err != nil {
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

	next := cloneJob(&cur)

	if err := change(ctx, tx, &next, c, now); err != nil {
		return job.Job{}, err
	}

	settle(&next, to, c, cur.Version, now)

	if err := closeLate(ctx, tx, &next, c.Event); err != nil {
		return job.Job{}, err
	}

	if err := afterChange(ctx, tx, &cur, &next, c.Event); err != nil {
		return job.Job{}, err
	}

	if err := update(ctx, tx, &next, cur.Version); err != nil {
		return job.Job{}, err
	}

	return next, nil
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

// settle sets the state, the version, the wait reason and the reason of next
// after the event of c moved the job from the version prev to state to.
func settle(next *job.Job, to job.State, c *job.Change, prev int64, now time.Time) {
	next.State, next.Version, next.UpdatedAt = to, prev+1, now

	next.Wait = job.WaitNone
	if to == job.StateQueued {
		next.Wait = c.Wait
	}

	if to.Terminal() {
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

		closed, err = observeIn(ctx, tx, &v, fence.Namespace)

		return err
	})
	if err != nil {
		return nil, err
	}

	return closed, nil
}

// observeIn closes the jobs that the final state version v closes, and
// records v as the last final version of its unit. It does nothing for a
// version that is not final.
func observeIn(ctx context.Context, tx Tx, v *job.StateVersion, ns namespace.Name) ([]job.Job, error) {
	if !v.Final {
		return nil, nil
	}

	rows, err := tx.Awaiting(ctx, ns, v.Unit.UnitID())
	if err != nil {
		return nil, err
	}

	closed, err := closeAwaiting(ctx, tx, ns, rows, v)
	if err != nil {
		return nil, err
	}

	err = tx.PutFinalVersion(ctx, ns, FinalVersion{
		UnitID: v.Unit.UnitID(), VersionID: v.VersionID, Commit: v.Commit, Job: v.Job, At: v.At,
	})

	return closed, err
}

// closeAwaiting closes each job of rows that v closes, and returns them. It
// refuses a row of another namespace than ns.
func closeAwaiting(
	ctx context.Context, tx Tx, ns namespace.Name, rows []Row, v *job.StateVersion,
) ([]job.Job, error) {
	now, nowErr := tx.Now(ctx)
	if nowErr != nil {
		return nil, nowErr
	}

	var closed []job.Job

	for i := range rows {
		// Defense in depth: the query of Awaiting filters by the namespace too;
		// this check stays so that a Tx that skips that filter cannot close the
		// job of another namespace.
		if rows[i].Namespace != ns {
			return nil, job.ErrNotFound
		}

		j, err := jobOf(&rows[i])
		if err != nil {
			return nil, err
		}

		if !j.ClosedBy(*v) {
			continue
		}

		if err := closeJob(ctx, tx, &j, *v); err != nil {
			return nil, err
		}

		prev := j.Version
		j.Version++
		j.UpdatedAt = now

		if err := update(ctx, tx, &j, prev); err != nil {
			return nil, err
		}

		closed = append(closed, j)
	}

	return closed, nil
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

	prev := j.Version
	j.Verification, j.Version, j.UpdatedAt = v, prev+1, now

	return true, update(ctx, tx, &j, prev)
}

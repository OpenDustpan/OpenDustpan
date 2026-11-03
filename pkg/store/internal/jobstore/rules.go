package jobstore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/manuallock"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// dispatchTimes holds the two times that the rules of a dispatch read: the
// oldest creation time of a plan that an apply phase may use, and the time
// of the dispatch on the clock of the database.
type dispatchTimes struct {
	notBefore time.Time
	now       time.Time
}

// checkLimits returns job.ErrLimit when the unit or its namespace has the
// largest count of open jobs.
func checkLimits(ctx context.Context, tx Tx, ns namespace.Name, unitID string) error {
	perUnit, err := tx.CountOpen(ctx, ns, unitID)
	if err != nil {
		return err
	}

	if perUnit >= job.MaxOpenPerUnit {
		return fmt.Errorf("%w: unit %s has %d open jobs", job.ErrLimit, unitID, perUnit)
	}

	perNamespace, err := tx.CountOpen(ctx, ns, "")
	if err != nil {
		return err
	}

	if perNamespace >= job.MaxOpenPerNamespace {
		return fmt.Errorf("%w: namespace %s has %d open jobs", job.ErrLimit, ns, perNamespace)
	}

	return nil
}

// startDispatch checks the rules of the dispatch of the current phase of j
// and starts its operation.
func startDispatch(ctx context.Context, tx Tx, j *job.Job, d *durable.StartRequest, at dispatchTimes) error {
	if err := checkDispatch(ctx, tx, j, at); err != nil {
		return err
	}

	if j.Current >= len(j.Phases) {
		return fmt.Errorf("%w: job %s has no phase to dispatch", job.ErrInvalid, j.ID)
	}

	op, err := tx.StartOperation(ctx, d)
	if err != nil {
		return fmt.Errorf("start %s: %w", d.Kind, err)
	}

	j.Phases[j.Current].Operation = op

	return nil
}

// checkDispatch checks the rules of the dispatch of the current phase of j
// at now: the manual lock, the unit, the freeze and the plans.
func checkDispatch(ctx context.Context, tx Tx, j *job.Job, at dispatchTimes) error {
	if j.Current >= len(j.Phases) {
		return nil
	}

	// The manual lock stops the dispatch of every phase, and no break-glass
	// passes it.
	if err := checkManualLock(ctx, tx, j); err != nil {
		return err
	}

	if !j.Phases[j.Current].Kind.Changes() {
		return nil
	}

	if err := checkUnit(ctx, tx, j); err != nil {
		return err
	}

	if err := checkFreeze(ctx, tx, j, at.now); err != nil {
		return err
	}

	return checkPlans(ctx, tx, j, at.notBefore)
}

// checkManualLock returns ErrLocked when a manual lock covers the unit of j.
// It reads the lock in the transaction of the dispatch, with the coverage of
// manuallock.Store.CoveringLock. It returns job.ErrUnitGone for a unit that is
// not in the inventory, and fails closed.
func checkManualLock(ctx context.Context, tx Tx, j *job.Job) error {
	unit := j.Spec.Unit

	// The lookup uses the stable unit ID, not the name that the spec stored at
	// the start: a rename of the unit must not hide its locks. A unit that
	// is not in the inventory fails the dispatch, because no lock can be read.
	locks, err := tx.CoveringLocks(ctx, unit.Namespace(), unit.UnitID())
	if errors.Is(err, ErrUnitNotInInventory) {
		return fmt.Errorf("check the manual locks of unit %s: %w: %w", unit, job.ErrUnitGone, err)
	}

	if err != nil {
		return fmt.Errorf("check the manual locks of unit %s: %w", unit, err)
	}

	if len(locks) == 0 {
		return nil
	}

	return fmt.Errorf(
		"%w: %w",
		job.ErrLocked,
		&manuallock.LockedError{
			Lock: locks[0],
			Unit: collection.UnitRef{Namespace: unit.Namespace(), Tool: unit.Tool(), Name: unit.UnitName()},
			Noun: "unit", Name: unit.String(),
		},
	)
}

// checkUnit returns ErrUnitBusy when another job holds the unit for a
// change. The partial unique index on the jobs table holds the same rule at
// the write; this read gives the answer before the write and names the job.
func checkUnit(ctx context.Context, tx Tx, j *job.Job) error {
	other, busy, err := tx.HolderOf(ctx, j.Spec.Unit.Namespace(), j.Spec.Unit.UnitID(), j.ID)
	if err != nil {
		return err
	}

	if busy {
		return fmt.Errorf("%w: job %s holds unit %s", job.ErrUnitBusy, other, j.Spec.Unit)
	}

	return nil
}

// checkFreeze returns ErrFrozen when a freeze of the namespace of j is
// active at now and j has no break-glass.
func checkFreeze(ctx context.Context, tx Tx, j *job.Job, now time.Time) error {
	if j.BreakGlass != nil {
		return nil
	}

	id, frozen, err := tx.ActiveFreeze(ctx, j.Spec.Unit.Namespace(), now)
	if err != nil {
		return err
	}

	if frozen {
		return fmt.Errorf("%w: %s", job.ErrFrozen, id)
	}

	return nil
}

// checkPlans checks the plans of the plan phase before the current phase
// of j, and the binding of each approval of the current phase.
func checkPlans(ctx context.Context, tx Tx, j *job.Job, notBefore time.Time) error {
	if j.Current == 0 || j.Phases[j.Current-1].Kind != job.PhasePlan {
		return nil
	}

	current, err := tx.StateVersion(ctx, j.Spec.Unit.Namespace(), j.Spec.Unit.UnitID())
	if err != nil {
		return err
	}

	plan := j.Phases[j.Current-1].Plan
	bindings := []*job.PlanBinding{nil}

	approvals := j.Phases[j.Current].Approvals
	for i := range approvals {
		if approvals[i].Plan != nil {
			bindings = append(bindings, approvals[i].Plan)
		}
	}

	for _, b := range bindings {
		if err := job.CheckPlan(plan, b, current, notBefore); err != nil {
			return fmt.Errorf("dispatch of phase %d: %w", j.Current, err)
		}
	}

	return nil
}

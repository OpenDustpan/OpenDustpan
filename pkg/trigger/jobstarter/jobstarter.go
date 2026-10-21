// Package jobstarter is the adapter of trigger.Starter over the job service. It
// is the only way that a trigger starts a job. It starts a plan on a runner and
// nothing else, because it has no path to any other operation: the operation
// and the executor are constants of this package, and a plan request has no
// field that could change them.
package jobstarter

import (
	"context"
	"errors"
	"fmt"

	"opendustpan.org/opendustpan/pkg/graph"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/trigger"
)

// Jobs is the part of the job service that the adapter uses. job.Service
// implements it.
type Jobs interface {
	// Start checks the request and the actions of the actor, and creates the
	// job.
	Start(ctx context.Context, a job.Actor, req job.StartRequest) (job.Job, error)
}

// Starter is the trigger.Starter over a job service.
type Starter struct {
	jobs  Jobs
	units trigger.Units
}

var _ trigger.Starter = (*Starter)(nil)

// New returns a Starter. It returns an error that wraps trigger.ErrInvalid
// when a port is nil.
func New(jobs Jobs, units trigger.Units) (*Starter, error) {
	if jobs == nil || units == nil {
		return nil, fmt.Errorf("%w: the starter of triggers needs the job service and units", trigger.ErrInvalid)
	}

	return &Starter{jobs: jobs, units: units}, nil
}

// StartPlan implements trigger.Starter. It resolves the unit to its current
// reference, and starts a plan of the runner executor for actor. The code
// location, the runner pool and the steps come from the settings of the unit,
// as for any start without them. The job records the trigger and its last
// saver, and keeps the source kind of the request.
//
// It returns job.ErrNotFound for a unit that no longer exists, and the errors
// of the job service for any refusal, for example job.ErrFrozen and
// job.ErrPolicyDenied. A manual lock on the unit does not make it fail: the job
// service queues the job.
func (s *Starter) StartPlan(ctx context.Context, actor job.Actor, unit graph.Unit, req trigger.PlanRequest,
) (job.ID, error) {
	if err := req.Trigger.Validate(); err != nil {
		return "", fmt.Errorf("start plan: %w", err)
	}

	obj, err := s.units.Object(ctx, unit)

	switch {
	case errors.Is(err, trigger.ErrUnitGone):
		return "", fmt.Errorf("start plan of %s: %w", unit, job.ErrNotFound)
	case err != nil:
		return "", fmt.Errorf("start plan of %s: %w", unit, err)
	}

	j, err := s.jobs.Start(ctx, actor, job.StartRequest{
		Unit: obj, Operation: job.OperationPlan, Executor: job.ExecutorRunner,
		Location: req.Location, Pool: "",
		Edits: nil, Delivery: "", Source: req.Source, IdempotencyKey: req.IdempotencyKey, Reason: req.Reason,
		BreakGlass: nil, Trigger: &job.TriggerRef{ID: string(req.Trigger), Saver: req.Saver},
		Channel: "", SourceIP: "", UserAgent: "", RequestID: "",
	})
	if err != nil {
		return "", fmt.Errorf("start plan of %s: %w", unit, err)
	}

	return j.ID, nil
}

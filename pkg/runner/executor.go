package runner

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"opendustpan.org/opendustpan/pkg/job"
)

// PoolReader reads one pool. PoolStore implements it.
type PoolReader interface {
	// Pool returns the pool id, or ErrNotFound.
	Pool(ctx context.Context, id string) (Pool, error)
}

// Executor is the job.Executor of the runners. It checks the spec of a runner
// job at start. The phases themselves run through the claims of a runner, so
// the executor has nothing to undo.
type Executor struct {
	pools PoolReader
}

var _ job.Executor = (*Executor)(nil)

// NewExecutor returns the Executor over pools. It returns ErrInvalid for a
// nil port.
func NewExecutor(pools PoolReader) (*Executor, error) {
	if pools == nil {
		return nil, fmt.Errorf("%w: the runner executor needs a pool reader", ErrInvalid)
	}

	return &Executor{pools: pools}, nil
}

// Kind implements job.Executor.
func (*Executor) Kind() job.ExecutorKind { return job.ExecutorRunner }

// Phases implements job.Executor. It returns the default phases of the
// operation of spec, job.ErrUnsupported when a runner cannot do the
// operation, and job.ErrInvalid, wrapped with the reason, for a spec with
// steps that fail job.ValidateSteps, with a step in a phase that the job does
// not have, with no pool, or with a pool of another namespace than the unit
// or one that runs no jobs. A pool that does not exist is job.ErrInvalid too.
func (e *Executor) Phases(ctx context.Context, spec job.Spec) ([]job.PhaseKind, error) {
	if spec.Executor != job.ExecutorRunner {
		return nil, fmt.Errorf("%w: the runner executor does not run %q jobs", job.ErrInvalid, spec.Executor)
	}

	kinds, err := job.DefaultPhases(job.ExecutorRunner, spec.Operation)
	if err != nil {
		return nil, fmt.Errorf("phases of the job: %w", err)
	}

	err = job.ValidateSteps(spec.Steps)
	if err != nil {
		return nil, fmt.Errorf("steps: %w", err)
	}

	for i := range spec.Steps {
		if !slices.Contains(kinds, spec.Steps[i].Phase) {
			return nil, fmt.Errorf("%w: step %q runs in the %s phase, which a %s job does not have", job.ErrInvalid,
				spec.Steps[i].Name, spec.Steps[i].Phase, spec.Operation)
		}
	}

	err = spec.Location.Validate()
	if err != nil {
		return nil, fmt.Errorf("code location: %w", err)
	}

	if spec.Pool == "" {
		return nil, fmt.Errorf("%w: a runner job needs a pool", job.ErrInvalid)
	}

	pool, err := e.pools.Pool(ctx, spec.Pool)

	switch {
	case errors.Is(err, ErrNotFound):
		return nil, fmt.Errorf("%w: pool %q does not exist", job.ErrInvalid, spec.Pool)
	case err != nil:
		return nil, fmt.Errorf("pool %s: %w", spec.Pool, err)
	}

	err = CheckJobPool(&pool, spec.Unit.Namespace())
	if err != nil {
		return nil, fmt.Errorf("%w: %w", job.ErrInvalid, err)
	}

	return kinds, nil
}

// Abandon implements job.Executor. A runner has nothing to undo.
func (*Executor) Abandon(context.Context, job.Job) error { return nil }

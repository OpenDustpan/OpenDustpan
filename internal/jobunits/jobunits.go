// Package jobunits adapts the job store to the identity port that answers the
// unit of the job of a job token. The rule of job tokens bounds every action
// of a token to that unit, and it reads the unit on every request, so this
// adapter is the only place that turns a job ID into a unit.
package jobunits

import (
	"context"
	"errors"
	"fmt"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
)

// ErrMissingStore reports a unit reader with no job store.
var ErrMissingStore = errors.New("jobunits: missing job store")

// Service answers the unit of a job from the job store.
type Service struct {
	jobs job.Store
}

var _ identity.JobUnits = (*Service)(nil)

// New returns the reader of the unit of a job over jobs. It returns
// ErrMissingStore when jobs is nil: a reader with no store would answer no
// unit, and the rule of job tokens then refuses every job token.
func New(jobs job.Store) (*Service, error) {
	if jobs == nil {
		return nil, ErrMissingStore
	}

	return &Service{jobs: jobs}, nil
}

// UnitOfJob implements identity.JobUnits. It returns the unit that the job
// holds, with its stable ID. It returns an error that wraps
// identity.ErrJobNotFound for an unknown job and for a job that ended: a job
// that ended holds no unit, so no token of it acts any more. Every other
// failure comes back wrapped, and the text names no job ID.
func (s *Service) UnitOfJob(ctx context.Context, jobID string) (identity.JobUnit, error) {
	j, err := s.jobs.Get(ctx, job.ID(jobID))
	if errors.Is(err, job.ErrNotFound) {
		return identity.JobUnit{}, fmt.Errorf("%w: the job does not exist", identity.ErrJobNotFound)
	}

	if err != nil {
		return identity.JobUnit{}, fmt.Errorf("read the job of the unit reader: %w", err)
	}

	// Defense in depth: the job token service refuses a token of a job that is
	// not live on every request. This check stays so that a reader that
	// answers the unit of an ended job cannot keep a token alive.
	if j.State.Terminal() {
		return identity.JobUnit{}, fmt.Errorf("%w: the job ended", identity.ErrJobNotFound)
	}

	unit, err := identity.NewJobUnit(j.Spec.Unit)
	if err != nil {
		return identity.JobUnit{}, fmt.Errorf("the unit of the job: %w", err)
	}

	return unit, nil
}

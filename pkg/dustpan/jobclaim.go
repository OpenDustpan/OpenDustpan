package dustpan

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
)

// jobClaimReader reads a job by its ID. The job store meets it.
type jobClaimReader interface {
	Get(ctx context.Context, id job.ID) (job.Job, error)
}

// jobClaimSubjects gives the Subject of a principal like its inner source,
// and for a job token it also sets the job claim, read from the job record on
// each call. A phase that changed after the last call gives its new claim.
type jobClaimSubjects struct {
	inner gate.SubjectSource
	jobs  jobClaimReader
}

// newJobClaimSubjects returns the subject source that adds the job claim of a
// job token to the subjects of inner. It is the only way to build one.
func newJobClaimSubjects(jobs jobClaimReader, inner gate.SubjectSource) jobClaimSubjects {
	return jobClaimSubjects{inner: inner, jobs: jobs}
}

// Subject implements gate.SubjectSource.
func (s jobClaimSubjects) Subject(ctx context.Context, p identity.Principal) (gate.Subject, error) {
	// Defense in depth: the state guard builder always sets the inner source, and
	// this check stays so that a zero value cannot panic or allow a write. It does
	// not rely on the builder.
	if s.inner == nil {
		return gate.Subject{}, fmt.Errorf("%w: the job claim source has no inner source", identity.ErrDenied)
	}

	subject, err := s.inner.Subject(ctx, p)
	if err != nil {
		return gate.Subject{}, err
	}

	if subject.Credential != gate.CredentialJobToken {
		return subject, nil
	}

	claim, err := s.claim(ctx, p.JobID)
	if err != nil {
		return gate.Subject{}, err
	}

	subject.Job = claim

	return subject, nil
}

// claim reads the job with the ID id and returns its claim. A job that does
// not resolve, an empty ID and a terminal job are denied.
func (s jobClaimSubjects) claim(ctx context.Context, id string) (*gate.JobClaim, error) {
	// A server with no job store issues no job claim: it fails closed.
	if s.jobs == nil {
		return nil, fmt.Errorf("%w: the server has no job store to read the job token", identity.ErrDenied)
	}
	// Defense in depth: the job token service gives each job token principal the
	// job ID of its token, and the job store finds no job with an empty ID. This
	// check stays so that a principal with no job ID never reaches the store. It
	// does not rely on the token service or on the store.
	if strings.TrimSpace(id) == "" {
		return nil, fmt.Errorf("%w: the job token names no job", identity.ErrDenied)
	}

	j, err := s.jobs.Get(ctx, job.ID(id))
	if errors.Is(err, job.ErrNotFound) {
		return nil, fmt.Errorf("%w: job %q of the job token does not exist", identity.ErrDenied, id)
	}

	if err != nil {
		return nil, fmt.Errorf("job %q: %w", id, err)
	}

	if j.State.Terminal() {
		return nil, fmt.Errorf("%w: job %q is %s and takes no state write", identity.ErrDenied, id, j.State)
	}

	if j.Current < 0 || j.Current >= len(j.Phases) {
		return nil, fmt.Errorf("%w: job %q has no phase at index %d", identity.ErrDenied, id, j.Current)
	}

	// A phase passed change.apply when it is the apply phase or a phase after it.
	// A job with no apply phase passes nothing, so it writes only its plan lock.
	passedApply := false
	for i := range j.Phases {
		if j.Phases[i].Kind == job.PhaseApply && j.Current >= i {
			passedApply = true
		}
	}

	return &gate.JobClaim{
		Job:         string(j.ID),
		Phase:       string(j.Phases[j.Current].Kind),
		PassedApply: passedApply,
		Units:       []identity.Object{j.Spec.Unit},
	}, nil
}

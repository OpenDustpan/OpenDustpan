package dustpan

import (
	"context"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// claimJobReader is the part of the job store that the release reads.
type claimJobReader interface {
	// Get returns the job id, or job.ErrNotFound.
	Get(ctx context.Context, id job.ID) (job.Job, error)
}

// newClaimReleasingClaimer wraps inner. With no registry the wrapper ends no
// claim: a server without claim routes holds none.
func newClaimReleasingClaimer(
	cfg *settings, claims *workspace.ClaimRegistry, inner job.Claimer,
) claimReleasingClaimer {
	clock := cfg.now
	if clock == nil {
		clock = time.Now
	}

	return claimReleasingClaimer{Claimer: inner, jobs: cfg.jobStore, claims: claims, now: clock}
}

// claimReleasingClaimer ends the apply claims of a job when the job reaches a
// terminal state. It wraps the claimer of the phases: after a phase ends
// through Finish or Release, it reads the job, and when the job is terminal
// it ends each accepted claim of the job in the registry, so the addresses of
// the job are free for the next claim. A job that is not terminal keeps its
// claims: its apply phase needs them.
type claimReleasingClaimer struct {
	job.Claimer

	jobs   claimJobReader
	claims *workspace.ClaimRegistry
	now    func() time.Time
}

// Finish implements job.Claimer.
func (c claimReleasingClaimer) Finish(ctx context.Context, pc job.PhaseClaim, o job.Outcome) error {
	if err := c.Claimer.Finish(ctx, pc, o); err != nil {
		return err
	}

	return c.releaseIfEnded(ctx, pc)
}

// Release implements job.Claimer.
func (c claimReleasingClaimer) Release(ctx context.Context, pc job.PhaseClaim) error {
	if err := c.Claimer.Release(ctx, pc); err != nil {
		return err
	}

	return c.releaseIfEnded(ctx, pc)
}

// releaseIfEnded ends the claims of the job of pc when the job is terminal.
// An error means that the claims may stay accepted: they block only the
// addresses of the job, so the failure is safe, and the caller sees it.
func (c claimReleasingClaimer) releaseIfEnded(ctx context.Context, pc job.PhaseClaim) error {
	if c.claims == nil || c.jobs == nil {
		return nil
	}

	// The phase has ended and committed. A canceled request must not skip the
	// release, so the call keeps the values of ctx and not its cancel.
	ctx = context.WithoutCancel(ctx)

	j, err := c.jobs.Get(ctx, pc.Job)
	if err != nil {
		return fmt.Errorf("read job %s to release its claims: %w", pc.Job, err)
	}

	if !j.State.Terminal() {
		return nil
	}

	if _, err = c.claims.ReleaseByJob(ctx, pc.Fence, string(pc.Job), c.now()); err != nil {
		return fmt.Errorf("release claims of job %s: %w", pc.Job, err)
	}

	return nil
}

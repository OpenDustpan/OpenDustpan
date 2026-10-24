package jobtest

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/job"
)

// errJobRead is the error of the first job read of a claimer over
// failFirstGet.
var errJobRead = errors.New("job read failed")

// failFirstGet is a job.Store whose first Get returns errJobRead. A claimer
// over it cannot run the operation that it claimed, so it gives the claim
// back.
type failFirstGet struct {
	job.Store

	once sync.Once
}

// Get implements job.Store.
func (s *failFirstGet) Get(ctx context.Context, id job.ID) (job.Job, error) {
	failed := false

	s.once.Do(func() { failed = true })

	if failed {
		return job.Job{}, errJobRead
	}

	return s.Store.Get(ctx, id)
}

// hookEnds is a durable.Store that runs hook once, before the first Release
// or GiveBack. The hook stands for a cancel that arrives after every read of
// the claimer and before its give-back writes.
type hookEnds struct {
	durable.Store

	once sync.Once
	hook func()
}

// Release implements durable.Store.
func (s *hookEnds) Release(ctx context.Context, c durable.Claim) error {
	s.once.Do(s.hook)

	return s.Store.Release(ctx, c)
}

// GiveBack implements durable.Store.
func (s *hookEnds) GiveBack(ctx context.Context, c durable.Claim) error {
	s.once.Do(s.hook)

	return s.Store.GiveBack(ctx, c)
}

// testGiveBackCancelBetween checks that a cancel that arrives just before the
// give-back of a claim leaves the operation running with its cancel, so that
// the claim after the expiry ends the operation and the job canceled. A
// give-back that ended the operation canceled would leave the job canceling
// with no operation to end it.
func testGiveBackCancelBetween(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	j := dispatched(t, h, f, plannedJob{unit: "", op: job.OperationPlan, steps: nil})
	a := claim(t, h, runnerOne, longRenew, runnerKind(&j))

	// The claim of runner-1 expires with the job running, so the next claim is a
	// takeover that reads a running job.
	lapse(t, h, &a)

	holder := job.NewClaimer(
		&failFirstGet{Store: h.Jobs, once: sync.Once{}},
		&hookEnds{Store: h.Durable, once: sync.Once{}, hook: func() { cancelJob(t, h, f, j.ID) }},
		h.Namespaces,
	)

	_, err := holder.Claim(t.Context(), request(runnerTwo, shortLease, runnerKind(&j)))
	if !errors.Is(err, errJobRead) {
		t.Fatalf("Claim with a failed job read = %v, want the read error", err)
	}

	op := opState(t, h, a.Claim)
	if op.State != durable.StateRunning || !op.CancelRequested || op.Claim.Owner != runnerTwo {
		t.Fatalf("operation after a give-back across a cancel = %s, cancel %v, owner %q; want running with the "+
			"cancel and the claim of runner-2", op.State, op.CancelRequested, op.Claim.Owner)
	}

	assertState(t, h, j.ID, job.StateCanceling)

	pause(t, time.Until(op.Claim.ExpiresAt)+lapses*pollEvery)

	noWork(t, h, runnerThree, runnerKind(&j), "after the claim that ends the canceled job")
	assertState(t, h, j.ID, job.StateCanceled)

	if op := opState(t, h, a.Claim); op.State != durable.StateCanceled {
		t.Fatalf("operation after the claim that ends the job = %s, want canceled", op.State)
	}
}

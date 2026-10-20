package jobtest

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/manuallock"
)

// racers is the number of callers that race in a case. It stays small: each
// racer holds one connection of the store.
const racers = 4

// lockRaceRounds is the number of rounds of the case that races a dispatch
// with a manual lock.
const lockRaceRounds = 10

// race runs fn on racers goroutines at the same time and returns the error of
// each one.
func race(fn func(i int) error) []error {
	var (
		wg    sync.WaitGroup
		start = make(chan struct{})
		errs  = make([]error, racers)
	)

	for i := range racers {
		wg.Go(func() {
			<-start

			errs[i] = fn(i)
		})
	}

	close(start)
	wg.Wait()

	return errs
}

// count returns the number of nil errors in errs, and the first error that is
// not nil and is not want.
func count(errs []error, want error) (int, error) {
	var (
		wins  int
		other error
	)

	for _, err := range errs {
		switch {
		case err == nil:
			wins++
		case !errors.Is(err, want) && other == nil:
			other = err
		default:
		}
	}

	return wins, other
}

// testTwoClaimers races several claimers for one phase. The durable store
// gives the operation to one of them. Of several writers of the claim of the
// job at one version, one wins and the others read the job again with
// ErrConflict. The phase records one claim.
func testTwoClaimers(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	j := runApplyPhase(t, h, f)
	kind := job.RunnerKind(j.Spec.Pool)

	errs := race(func(i int) error {
		_, err := h.Durable.Claim(t.Context(), durable.ClaimRequest{
			Owner: owner(i), Kinds: []durable.Kind{kind}, MaxVersions: nil, Lease: time.Minute,
		})

		return err
	})

	if wins, other := count(errs, durable.ErrNoWork); wins != 1 || other != nil {
		t.Fatalf("durable claims by %d racers = %d wins, other error %v; want 1 win, the rest ErrNoWork", racers,
			wins, other)
	}

	errs = race(func(i int) error {
		c := change(&j, job.EventClaim)
		c.Executor = owner(i)

		_, err := h.Jobs.Apply(t.Context(), c, f.fence, []audit.Entry{entry(f.unit, job.ActionJobRead)})

		return err
	})

	if wins, other := count(errs, job.ErrConflict); wins != 1 || other != nil {
		t.Fatalf("claims of the job by %d racers = %d wins, other error %v; want 1 win, the rest ErrConflict",
			racers, wins, other)
	}

	got, err := h.Jobs.Get(t.Context(), j.ID)
	if err != nil || got.State != job.StateRunning || got.Phases[0].Claims != 1 || got.Version != j.Version+1 {
		t.Fatalf("job after the race = %s, %d claims, version %d, %v; want running, 1 claim, version %d",
			got.State, got.Phases[0].Claims, got.Version, err, j.Version+1)
	}

	entries, err := h.Domain.AuditEntries(t.Context(), f.fence.Namespace)
	if err != nil || len(entries) < 1 {
		t.Fatalf("AuditEntries = %d, %v; want the entry of the winner", len(entries), err)
	}
}

func owner(i int) string {
	return fmt.Sprintf("runner-%d", i)
}

// testParallelUnitBusy dispatches the change phase of several jobs of one
// unit at the same time. One job holds the unit, and every other dispatch is
// ErrUnitBusy and leaves its job queued.
func testParallelUnitBusy(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	jobs := make([]job.Job, racers)

	for i := range jobs {
		jobs[i] = create(t, h, f, newJob(f.unit, job.OperationRefresh, ""), nil)
	}

	errs := race(func(i int) error {
		_, err := h.Jobs.Apply(t.Context(), dispatch(&jobs[i]), f.fence, nil)

		return err
	})

	if wins, other := count(errs, job.ErrUnitBusy); wins != 1 || other != nil {
		t.Fatalf("dispatches by %d racers = %d wins, other error %v; want 1 win, the rest ErrUnitBusy", racers,
			wins, other)
	}

	dispatched := 0

	for i := range jobs {
		got, err := h.Jobs.Get(t.Context(), jobs[i].ID)
		if err != nil {
			t.Fatalf("Get = %v", err)
		}

		if got.State == job.StateDispatched {
			dispatched++
		}
	}

	if dispatched != 1 {
		t.Fatalf("%d jobs are dispatched, want 1", dispatched)
	}
}

// testDispatchAndLockInParallel races the dispatch of a job with the manual
// lock of its unit. The outcome is one of the two serial orders: the dispatch
// ran first and the job is dispatched, or the lock came first and the dispatch
// is ErrLocked with the job still queued. A dispatch never fails in another
// way, and a job never runs on a locked unit that the dispatch read as free.
func testDispatchAndLockInParallel(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	target := manuallock.UnitTarget(collection.UnitRef{
		Namespace: f.unit.Namespace(), Tool: f.unit.Tool(), Name: f.unit.UnitName(),
	})

	for range lockRaceRounds {
		j := create(t, h, f, newJob(f.unit, job.OperationPlan, ""), nil)
		got := raceDispatchAndLock(t, h, f, &j, target)

		if got.lock != nil {
			t.Fatalf("PutManualLock = %v", got.lock)
		}

		want := job.StateDispatched
		if got.dispatch != nil {
			want = job.StateQueued
		}

		if got.dispatch != nil && !errors.Is(got.dispatch, job.ErrLocked) {
			t.Fatalf("dispatch racing a lock = %v, want nil or ErrLocked", got.dispatch)
		}

		assertState(t, h, j.ID, want)

		// With the lock in place, a later dispatch is always ErrLocked.
		next := create(t, h, f, newJob(f.unit, job.OperationPlan, ""), nil)
		if _, err := h.Jobs.Apply(t.Context(), dispatch(&next), f.fence, nil); !errors.Is(err, job.ErrLocked) {
			t.Fatalf("dispatch after the lock = %v, want ErrLocked", err)
		}

		unlockUnit(t, h, f)
		cancelJobs(t, h, f, j.ID, next.ID)
	}
}

// raceResult holds the error of each call of a race between a dispatch and a
// lock.
type raceResult struct {
	dispatch error
	lock     error
}

// raceDispatchAndLock dispatches j and locks target at the same time, and
// returns the error of each call.
func raceDispatchAndLock(t *testing.T, h Harness, f fixture, j *job.Job, target manuallock.Target) raceResult {
	t.Helper()

	var (
		wg    sync.WaitGroup
		start = make(chan struct{})
		out   raceResult
	)

	wg.Go(func() {
		<-start

		_, out.dispatch = h.Jobs.Apply(t.Context(), dispatch(j), f.fence, nil)
	})

	wg.Go(func() {
		<-start

		out.lock = h.Domain.PutManualLock(t.Context(), f.fence, target, lockHolder, lockReason)
	})

	close(start)
	wg.Wait()

	return out
}

// cancelJobs ends the jobs, so that the rounds of a case stay below the limit
// of open jobs.
func cancelJobs(t *testing.T, h Harness, f fixture, ids ...job.ID) {
	t.Helper()

	for _, id := range ids {
		open, err := h.Jobs.Get(t.Context(), id)
		if err != nil {
			t.Fatalf("Get = %v", err)
		}

		apply(t, h, f, change(&open, job.EventCancel))
	}
}

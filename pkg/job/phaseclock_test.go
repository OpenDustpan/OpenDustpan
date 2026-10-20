package job_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/manuallock"
)

var errStoreDown = errors.New("job store down")

// getStore answers Get from a map. Any other method of the port panics on the
// nil embedded interface, which proves the clock reads nothing else.
type getStore struct {
	job.Store

	jobs map[job.ID]job.Job
	err  error
}

func (s getStore) Get(_ context.Context, id job.ID) (job.Job, error) {
	if s.err != nil {
		return job.Job{}, s.err
	}

	j, ok := s.jobs[id]
	if !ok {
		return job.Job{}, job.ErrNotFound
	}

	return j, nil
}

// phaseJob returns the job job-1 of unit in state, with one phase that started at
// started, and cur as its current phase.
func phaseJob(unit identity.Object, state job.State, cur int, started time.Time) job.Job {
	j := jobIn(state, nil)
	j.ID = "job-1"
	j.Spec.Unit = unit
	j.Current = cur
	j.Phases = []job.Phase{phaseOf(job.PhaseApply, 1, nil)}
	j.Phases[0].StartedAt = started

	return j
}

func TestPhaseClock(t *testing.T) {
	t.Parallel()

	started := time.Date(2026, time.October, 19, 10, 0, 0, 0, time.UTC)
	unit, err := identity.UnitObject("opentofu", "acme", "network")
	if err != nil {
		t.Fatalf("UnitObject: %v", err)
	}

	other, err := identity.UnitObject("opentofu", "acme", "dns")
	if err != nil {
		t.Fatalf("UnitObject: %v", err)
	}

	ref := collection.UnitRef{Namespace: "acme", Tool: "opentofu", Name: "network"}
	one := func(j job.Job) map[job.ID]job.Job { return map[job.ID]job.Job{"job-1": j} }

	// Each case: name, the jobs of the store, the error of the store, the
	// expected error (nil for an answer), and the expected start.
	cases := []struct {
		name    string
		jobs    map[job.ID]job.Job
		storeEr error
		want    error
		wantAt  time.Time
	}{
		{"running phase", one(phaseJob(unit, job.StateRunning, 0, started)), nil, nil, started},
		{"dispatched phase", one(phaseJob(unit, job.StateDispatched, 0, started)), nil, nil, started},
		{"canceling phase", one(phaseJob(unit, job.StateCanceling, 0, started)), nil, nil, started},
		{"queued job", one(phaseJob(unit, job.StateQueued, 0, started)), nil, manuallock.ErrNoPhase, time.Time{}},
		{"finished job", one(phaseJob(unit, job.StateSucceeded, 0, started)), nil, manuallock.ErrNoPhase, time.Time{}},
		{
			"job of another unit",
			one(phaseJob(other, job.StateRunning, 0, started)),
			nil,
			manuallock.ErrNoPhase,
			time.Time{},
		},
		{
			"phase out of range",
			one(phaseJob(unit, job.StateRunning, 3, started)),
			nil,
			manuallock.ErrNoPhase,
			time.Time{},
		},
		{"unknown job", map[job.ID]job.Job{}, nil, manuallock.ErrNoPhase, time.Time{}},
		{"store fails", nil, errStoreDown, errStoreDown, time.Time{}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			clock := job.PhaseClock{Jobs: getStore{Store: nil, jobs: tc.jobs, err: tc.storeEr}}

			got, err := clock.PhaseStartedAt(t.Context(), "job-1", ref)
			if !errors.Is(err, tc.want) || !got.Equal(tc.wantAt) {
				t.Fatalf("got %v, %v; want %v, %v", got, err, tc.wantAt, tc.want)
			}
		})
	}
}

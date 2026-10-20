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

	mk := func(u identity.Object, state job.State, cur int) job.Job {
		return job.Job{
			ID: "job-1", Spec: job.Spec{Unit: u}, State: state, Current: cur,
			Phases: []job.Phase{{StartedAt: started}},
		}
	}

	cases := []struct {
		name    string
		store   getStore
		want    error
		wantAt  time.Time
		wantAny bool
	}{
		{name: "running phase", store: getStore{jobs: map[job.ID]job.Job{"job-1": mk(unit, job.StateRunning, 0)}}, wantAt: started},
		{name: "dispatched phase", store: getStore{jobs: map[job.ID]job.Job{"job-1": mk(unit, job.StateDispatched, 0)}}, wantAt: started},
		{name: "canceling phase", store: getStore{jobs: map[job.ID]job.Job{"job-1": mk(unit, job.StateCanceling, 0)}}, wantAt: started},
		{
			name: "queued job", want: manuallock.ErrNoPhase,
			store: getStore{jobs: map[job.ID]job.Job{"job-1": mk(unit, job.StateQueued, 0)}},
		},
		{
			name: "finished job", want: manuallock.ErrNoPhase,
			store: getStore{jobs: map[job.ID]job.Job{"job-1": mk(unit, job.StateSucceeded, 0)}},
		},
		{
			name: "job of another unit", want: manuallock.ErrNoPhase,
			store: getStore{jobs: map[job.ID]job.Job{"job-1": mk(other, job.StateRunning, 0)}},
		},
		{
			name: "phase out of range", want: manuallock.ErrNoPhase,
			store: getStore{jobs: map[job.ID]job.Job{"job-1": mk(unit, job.StateRunning, 3)}},
		},
		{name: "unknown job", want: manuallock.ErrNoPhase, store: getStore{jobs: map[job.ID]job.Job{}}},
		{name: "store fails", wantAny: true, store: getStore{err: errStoreDown}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := job.PhaseClock{Jobs: tc.store}.PhaseStartedAt(t.Context(), "job-1", ref)

			switch {
			case tc.wantAny:
				if !errors.Is(err, errStoreDown) {
					t.Fatalf("err = %v, want the store error", err)
				}
			case tc.want != nil:
				if !errors.Is(err, tc.want) || !got.IsZero() {
					t.Fatalf("got %v, %v; want %v", got, err, tc.want)
				}
			default:
				if err != nil || !got.Equal(tc.wantAt) {
					t.Fatalf("got %v, %v; want %v", got, err, tc.wantAt)
				}
			}
		})
	}
}

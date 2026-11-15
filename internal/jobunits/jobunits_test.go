package jobunits_test

import (
	"context"
	"errors"
	"testing"

	"opendustpan.org/opendustpan/internal/jobunits"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// errStoreDown is a job store that cannot answer.
var errStoreDown = errors.New("the job store is down")

// fakeJobs is a job.Store that answers the jobs that the test sets. A lookup
// of a job that it does not hold answers job.ErrNotFound, as an adapter does.
type fakeJobs struct {
	job.Store

	jobs map[job.ID]job.Job
	err  error
}

// Get implements job.Store.
func (f *fakeJobs) Get(_ context.Context, id job.ID) (job.Job, error) {
	if f.err != nil {
		return job.Job{}, f.err
	}

	j, ok := f.jobs[id]
	if !ok {
		return job.Job{}, job.ErrNotFound
	}

	return j, nil
}

// worldUnit returns the unit of namespace ns with the stable ID unitID.
func worldUnit(t *testing.T, ns namespace.Name, unitID string) identity.Object {
	t.Helper()

	unit, err := identity.UnitObject("pulumi", ns, "web/prod")
	if err != nil {
		t.Fatalf("unit: %v", err)
	}

	return unit.WithUnitID(unitID)
}

// worldJob returns a job that acts on unit and that holds state.
func worldJob(id job.ID, unit identity.Object, state job.State) job.Job {
	var j job.Job

	j.ID = id
	j.Spec.Unit = unit
	j.State = state

	return j
}

// unitCase is one row of the table of the reader.
type unitCase struct {
	name     string
	store    job.Store
	jobID    string
	wantErr  error
	wantUnit identity.Object
}

// TestUnitOfJob proves what the reader of the unit of a job answers. The rule
// of job tokens bounds every action of a token with the unit that this port
// answers, so each case here sends hostile input straight to the adapter: an
// answer that is too wide, or an answer for a job that no longer exists, would
// widen a token or keep it alive.
func TestUnitOfJob(t *testing.T) {
	t.Parallel()

	ns, err := namespace.ParseName("acme")
	if err != nil {
		t.Fatalf("namespace: %v", err)
	}

	live := worldUnit(t, ns, "unt-live")
	ended := worldUnit(t, ns, "unt-ended")

	noID, err := identity.UnitObject("pulumi", ns, "web/dev")
	if err != nil {
		t.Fatalf("unit: %v", err)
	}

	liveJob := worldJob("job-live", live, job.StateRunning)
	endedJob := worldJob("job-ended", ended, job.StateSucceeded)
	noIDJob := worldJob("job-no-id", noID, job.StateRunning)

	all := &fakeJobs{
		Store: nil,
		jobs: map[job.ID]job.Job{
			liveJob.ID:  liveJob,
			endedJob.ID: endedJob,
			noIDJob.ID:  noIDJob,
		},
		err: nil,
	}

	tests := []unitCase{
		{
			name: "the unit of a live job", store: all, jobID: string(liveJob.ID),
			wantErr: nil, wantUnit: live,
		},
		{
			name: "a job that ended", store: all, jobID: string(endedJob.ID),
			wantErr: identity.ErrJobNotFound, wantUnit: identity.Object{},
		},
		{
			name: "an unknown job", store: all, jobID: "job-missing",
			wantErr: identity.ErrJobNotFound, wantUnit: identity.Object{},
		},
		{
			name: "a job whose unit holds no stable ID", store: all, jobID: string(noIDJob.ID),
			wantErr: identity.ErrInvalidObject, wantUnit: identity.Object{},
		},
		{
			name: "a store that fails", store: &fakeJobs{Store: nil, jobs: nil, err: errStoreDown},
			jobID: "job-live", wantErr: errStoreDown, wantUnit: identity.Object{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			checkUnitCase(t, tc)
		})
	}
}

// checkUnitCase proves one case of the table of the reader.
func checkUnitCase(t *testing.T, tc unitCase) {
	t.Helper()

	units, err := jobunits.New(tc.store)
	if err != nil {
		t.Fatalf("unit reader: %v", err)
	}

	got, err := units.UnitOfJob(t.Context(), tc.jobID)

	if tc.wantErr != nil {
		checkUnitError(t, got, err, tc.wantErr)

		return
	}

	if err != nil {
		t.Fatalf("UnitOfJob = %v, want no error", err)
	}

	if got.Object() != tc.wantUnit {
		t.Errorf("UnitOfJob = %q, want %q", got.Object(), tc.wantUnit)
	}

	if got.UnitID() != tc.wantUnit.UnitID() {
		t.Errorf("UnitOfJob unit ID = %q, want %q", got.UnitID(), tc.wantUnit.UnitID())
	}
}

// checkUnitError proves the error and the unit of a refused read.
func checkUnitError(t *testing.T, got identity.JobUnit, err, want error) {
	t.Helper()

	if !errors.Is(err, want) {
		t.Fatalf("UnitOfJob = %v, want an error that wraps %v", err, want)
	}

	if errors.Is(err, identity.ErrJobNotFound) && !errors.Is(want, identity.ErrJobNotFound) {
		t.Errorf("UnitOfJob = %v, want no ErrJobNotFound", err)
	}

	if got != (identity.JobUnit{}) {
		t.Errorf("UnitOfJob = %q, want no unit", got.Object())
	}
}

// TestNewRefusesAReaderWithoutAStore proves that the adapter fails at the
// build, so the rule of job tokens never reads a port that answers no unit.
func TestNewRefusesAReaderWithoutAStore(t *testing.T) {
	t.Parallel()

	if _, err := jobunits.New(nil); !errors.Is(err, jobunits.ErrMissingStore) {
		t.Fatalf("unit reader without a store = %v, want ErrMissingStore", err)
	}
}

package dustpan

import (
	"context"
	"errors"
	"maps"
	"testing"

	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
)

var errDiskGone = errors.New("disk gone")

// fakeJobReader reads jobs from a map, as the job store does for one ID.
type fakeJobReader struct {
	jobs map[job.ID]job.Job
}

func (f fakeJobReader) Get(_ context.Context, id job.ID) (job.Job, error) {
	j, ok := f.jobs[id]
	if !ok {
		return job.Job{}, job.ErrNotFound
	}

	return j, nil
}

// failingJobReader returns one error for each job.
type failingJobReader struct {
	err error
}

func (f failingJobReader) Get(_ context.Context, _ job.ID) (job.Job, error) {
	return job.Job{}, f.err
}

// fakeInnerSubjects gives the subject of a job token with no job, as
// principalSubjects does on main.
type fakeInnerSubjects struct{}

func (fakeInnerSubjects) Subject(_ context.Context, p identity.Principal) (gate.Subject, error) {
	var s gate.Subject

	s.ID = p.Subject
	s.Credential = gate.CredentialSession

	if p.Credential == identity.CredentialJobToken {
		s.Credential = gate.CredentialJobToken
	}

	return s, nil
}

// testPlanJob returns a running job with a plan phase and an apply phase.
func testPlanJob(id job.ID, current int) job.Job {
	var plan, apply job.Phase

	plan.Kind = job.PhasePlan
	apply.Kind = job.PhaseApply

	var j job.Job

	j.ID = id
	j.State = job.StateRunning
	j.Phases = []job.Phase{plan, apply}
	j.Current = current

	return j
}

func claimTestPrincipal(subject, jobID string) identity.Principal {
	var p identity.Principal

	p.Subject = subject
	p.Credential = identity.CredentialJobToken
	p.JobID = jobID

	return p
}

func TestJobClaimSubjectsResolvesJob(t *testing.T) {
	t.Parallel()

	reader := fakeJobReader{jobs: map[job.ID]job.Job{"job-plan": testPlanJob("job-plan", 0)}}
	src := newJobClaimSubjects(reader, fakeInnerSubjects{})

	got, err := src.Subject(t.Context(), claimTestPrincipal("token-1", "job-plan"))
	if err != nil {
		t.Fatalf("Subject: %v", err)
	}

	if got.Job == nil || got.Job.Job != "job-plan" || got.Job.Phase != string(job.PhasePlan) ||
		got.Job.PassedApply || len(got.Job.Units) != 1 {
		t.Fatalf("Subject job = %+v, want the plan claim of job-plan", got.Job)
	}
}

func TestJobClaimSubjectsDenied(t *testing.T) {
	t.Parallel()

	terminal := testPlanJob("job-done", 0)
	terminal.State = job.StateSucceeded

	canceled := testPlanJob("job-canceled", 0)
	canceled.State = job.StateCanceled

	// A job that does not hold its unit has no claim, also with Current at the
	// apply phase (index 1).
	waiting := map[job.ID]job.State{
		"job-queued": job.StateQueued, "job-approval": job.StateAwaitingApproval,
		"job-awaiting-state": job.StateAwaitingState, "job-empty-state": "", "job-unknown-state": "mystery",
	}
	notLive := map[job.ID]job.Job{}

	for id, state := range waiting {
		w := testPlanJob(id, 1)
		w.State = state
		notLive[id] = w
	}

	beforeStart := testPlanJob("job-before", -1)
	atEnd := testPlanJob("job-at-end", 2)
	pastEnd := testPlanJob("job-past", 3)

	// The reader holds a running job under an empty and a blank ID, so only
	// the check of the job ID refuses these principals.
	emptyID := testPlanJob("", 1)
	blankID := testPlanJob("  ", 1)

	tests := []struct {
		name  string
		jobID string
	}{
		{name: "an unknown job is denied", jobID: "job-missing"},
		{name: "an empty job ID is denied", jobID: ""},
		{name: "a blank job ID is denied", jobID: "  "},
		{name: "a terminal job is denied", jobID: "job-done"},
		{name: "a canceled job is denied", jobID: "job-canceled"},
		{name: "a queued job at the apply phase is denied", jobID: "job-queued"},
		{name: "a job that waits for approval at the apply phase is denied", jobID: "job-approval"},
		{name: "a job that waits for a state at the apply phase is denied", jobID: "job-awaiting-state"},
		{name: "a job with an empty state is denied", jobID: "job-empty-state"},
		{name: "a job with an unknown state is denied", jobID: "job-unknown-state"},
		{name: "a phase index before the first phase is denied", jobID: "job-before"},
		{name: "a phase index equal to the number of phases is denied", jobID: "job-at-end"},
		{name: "a phase index past the last phase is denied", jobID: "job-past"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Each subtest has its own reader, so the check after the call reads
			// only what this subtest wrote.
			reader := fakeJobReader{jobs: map[job.ID]job.Job{
				"job-done": terminal, "job-canceled": canceled, "job-before": beforeStart, "job-at-end": atEnd,
				"job-past": pastEnd, "": emptyID, "  ": blankID,
			}}
			maps.Copy(reader.jobs, notLive)
			src := newJobClaimSubjects(reader, fakeInnerSubjects{})

			got, err := src.Subject(t.Context(), claimTestPrincipal("token-1", tt.jobID))
			if !errors.Is(err, identity.ErrDenied) {
				t.Fatalf("Subject error = %v, want identity.ErrDenied", err)
			}

			if got.Job != nil {
				t.Fatalf("Subject job = %+v, want no claim with a denial", got.Job)
			}

			// A refusal reads the job and changes no job record.
			if after := reader.jobs["job-canceled"]; after.State != job.StateCanceled || after.Current != 0 ||
				len(after.Phases) != 2 {
				t.Fatalf("canceled job after the call = %+v, want it unchanged", after)
			}
		})
	}
}

func TestJobClaimSubjectsZeroValueDenies(t *testing.T) {
	t.Parallel()

	var src jobClaimSubjects

	got, err := src.Subject(t.Context(), claimTestPrincipal("token-5", "job-plan"))
	if !errors.Is(err, identity.ErrDenied) {
		t.Fatalf("Subject error = %v, want identity.ErrDenied", err)
	}

	if got.Job != nil || got.Credential != "" {
		t.Fatalf("Subject = %+v, want the zero subject", got)
	}
}

func TestJobClaimSubjectsPhaseChange(t *testing.T) {
	t.Parallel()

	store := fakeJobReader{jobs: map[job.ID]job.Job{"job-plan": testPlanJob("job-plan", 0)}}
	src := newJobClaimSubjects(store, fakeInnerSubjects{})
	p := claimTestPrincipal("token-2", "job-plan")

	first, err := src.Subject(t.Context(), p)
	if err != nil {
		t.Fatalf("first Subject: %v", err)
	}

	if first.Job == nil || first.Job.Phase != string(job.PhasePlan) {
		t.Fatalf("first phase = %+v, want plan", first.Job)
	}

	store.jobs["job-plan"] = testPlanJob("job-plan", 1)

	second, err := src.Subject(t.Context(), p)
	if err != nil {
		t.Fatalf("second Subject: %v", err)
	}

	if second.Job == nil || second.Job.Phase != string(job.PhaseApply) || !second.Job.PassedApply {
		t.Fatalf("second claim = %+v, want apply that passed change.apply", second.Job)
	}
}

func TestJobClaimSubjectsNoClaimForOtherCredential(t *testing.T) {
	t.Parallel()

	src := newJobClaimSubjects(fakeJobReader{jobs: nil}, fakeInnerSubjects{})

	var p identity.Principal

	p.Subject = "s"
	p.Credential = identity.CredentialSession

	got, err := src.Subject(t.Context(), p)
	if err != nil {
		t.Fatalf("Subject: %v", err)
	}

	if got.Job != nil {
		t.Fatalf("Subject job = %+v, want nil", got.Job)
	}
}

func TestJobClaimSubjectsNoJobStoreDenies(t *testing.T) {
	t.Parallel()

	src := newJobClaimSubjects(nil, fakeInnerSubjects{})

	_, err := src.Subject(t.Context(), claimTestPrincipal("token-4", "job-plan"))
	if !errors.Is(err, identity.ErrDenied) {
		t.Fatalf("Subject error = %v, want identity.ErrDenied", err)
	}
}

// failingInnerSubjects gives the subject of a job token together with an
// error, so that only the check of the inner error refuses the principal.
type failingInnerSubjects struct{}

func (failingInnerSubjects) Subject(_ context.Context, p identity.Principal) (gate.Subject, error) {
	var s gate.Subject

	s.ID = p.Subject
	s.Credential = gate.CredentialJobToken

	return s, errDiskGone
}

func TestJobClaimSubjectsInnerErrorIsReturned(t *testing.T) {
	t.Parallel()

	reader := fakeJobReader{jobs: map[job.ID]job.Job{"job-plan": testPlanJob("job-plan", 1)}}
	src := newJobClaimSubjects(reader, failingInnerSubjects{})

	got, err := src.Subject(t.Context(), claimTestPrincipal("token-6", "job-plan"))
	if !errors.Is(err, errDiskGone) {
		t.Fatalf("Subject error = %v, want the error of the inner source", err)
	}

	if got.Job != nil || got.Credential != "" {
		t.Fatalf("Subject = %+v, want the zero subject with the inner error", got)
	}
}

func TestJobClaimSubjectsStoreErrorIsNotDenied(t *testing.T) {
	t.Parallel()

	src := newJobClaimSubjects(failingJobReader{err: errDiskGone}, fakeInnerSubjects{})

	_, err := src.Subject(t.Context(), claimTestPrincipal("token-3", "job-plan"))
	if !errors.Is(err, errDiskGone) || errors.Is(err, identity.ErrDenied) {
		t.Fatalf("Subject error = %v, want a wrapped store error that is not identity.ErrDenied", err)
	}
}

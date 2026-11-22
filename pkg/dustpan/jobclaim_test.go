package dustpan

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
)

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

// fakeInnerSubjects gives the subject of a job token with no job, as
// principalSubjects does on main.
type fakeInnerSubjects struct{}

func (fakeInnerSubjects) Subject(_ context.Context, p identity.Principal) (gate.Subject, error) {
	credential := gate.CredentialSession
	if p.Credential == identity.CredentialJobToken {
		credential = gate.CredentialJobToken
	}

	return gate.Subject{ID: p.Subject, Credential: credential, Job: nil}, nil
}

func TestJobClaimSubjects(t *testing.T) {
	t.Parallel()

	unit := identity.Object{}
	planJob := job.Job{
		ID:    "job-plan",
		State: job.StateRunning,
		Spec:  job.Spec{Unit: unit},
		Phases: []job.Phase{
			{Kind: job.PhasePlan},
			{Kind: job.PhaseApply},
		},
		Current: 0,
	}
	applyJob := planJob
	applyJob.ID = "job-apply"
	applyJob.Current = 1

	terminalJob := planJob
	terminalJob.ID = "job-done"
	terminalJob.State = job.StateSucceeded

	reader := fakeJobReader{jobs: map[job.ID]job.Job{
		planJob.ID:     planJob,
		applyJob.ID:    applyJob,
		terminalJob.ID: terminalJob,
	}}

	tests := []struct {
		name    string
		jobID   string
		wantErr bool
		want    *gate.JobClaim
	}{
		{
			name:  "a job that resolves gives the claim",
			jobID: "job-plan",
			want:  &gate.JobClaim{Job: "job-plan", Phase: "plan", PassedApply: false, Units: []identity.Object{unit}},
		},
		{
			name:    "an unknown job is denied",
			jobID:   "job-missing",
			wantErr: true,
		},
		{
			name:    "an empty job ID is denied",
			jobID:   "",
			wantErr: true,
		},
		{
			name:    "a terminal job is denied",
			jobID:   "job-done",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			src := newJobClaimSubjects(reader, fakeInnerSubjects{})

			got, err := src.Subject(t.Context(), identity.Principal{
				Subject: "token-1", Credential: identity.CredentialJobToken, JobID: tt.jobID,
			})
			if tt.wantErr {
				if !errors.Is(err, identity.ErrDenied) {
					t.Fatalf("Subject error = %v, want identity.ErrDenied", err)
				}

				return
			}

			if err != nil {
				t.Fatalf("Subject: %v", err)
			}

			if got.Job == nil || got.Job.Job != tt.want.Job || got.Job.Phase != tt.want.Phase ||
				got.Job.PassedApply != tt.want.PassedApply || len(got.Job.Units) != len(tt.want.Units) {
				t.Fatalf("Subject job = %+v, want %+v", got.Job, tt.want)
			}
		})
	}

	t.Run("a second call after a phase change gives the new phase", func(t *testing.T) {
		t.Parallel()

		store := fakeJobReader{jobs: map[job.ID]job.Job{planJob.ID: planJob}}
		src := newJobClaimSubjects(store, fakeInnerSubjects{})
		p := identity.Principal{Subject: "token-2", Credential: identity.CredentialJobToken, JobID: "job-plan"}

		first, err := src.Subject(t.Context(), p)
		if err != nil {
			t.Fatalf("first Subject: %v", err)
		}

		if first.Job == nil || first.Job.Phase != string(job.PhasePlan) {
			t.Fatalf("first phase = %+v, want plan", first.Job)
		}

		moved := planJob
		moved.Current = 1
		store.jobs[planJob.ID] = moved

		second, err := src.Subject(t.Context(), p)
		if err != nil {
			t.Fatalf("second Subject: %v", err)
		}

		if second.Job == nil || second.Job.Phase != string(job.PhaseApply) || !second.Job.PassedApply {
			t.Fatalf("second claim = %+v, want apply that passed change.apply", second.Job)
		}
	})

	t.Run("a principal that is not a job token gets no claim", func(t *testing.T) {
		t.Parallel()

		src := newJobClaimSubjects(reader, fakeInnerSubjects{})

		got, err := src.Subject(t.Context(), identity.Principal{Subject: "s", Credential: identity.CredentialSession})
		if err != nil {
			t.Fatalf("Subject: %v", err)
		}

		if got.Job != nil {
			t.Fatalf("Subject job = %+v, want nil", got.Job)
		}
	})

	t.Run("a server with no job store denies a job token", func(t *testing.T) {
		t.Parallel()

		src := newJobClaimSubjects(nil, fakeInnerSubjects{})

		_, err := src.Subject(t.Context(), identity.Principal{
			Subject: "token-4", Credential: identity.CredentialJobToken, JobID: "job-plan",
		})
		if !errors.Is(err, identity.ErrDenied) {
			t.Fatalf("Subject error = %v, want identity.ErrDenied", err)
		}
	})

	t.Run("a store error is returned wrapped, not as a driver error", func(t *testing.T) {
		t.Parallel()

		src := newJobClaimSubjects(failingJobReader{err: fmt.Errorf("disk gone")}, fakeInnerSubjects{})

		_, err := src.Subject(t.Context(), identity.Principal{
			Subject: "token-3", Credential: identity.CredentialJobToken, JobID: "job-plan",
		})
		if err == nil || errors.Is(err, identity.ErrDenied) {
			t.Fatalf("Subject error = %v, want a wrapped store error that is not identity.ErrDenied", err)
		}
	})
}

// failingJobReader returns one error for each job.
type failingJobReader struct {
	err error
}

func (f failingJobReader) Get(_ context.Context, _ job.ID) (job.Job, error) {
	return job.Job{}, f.err
}

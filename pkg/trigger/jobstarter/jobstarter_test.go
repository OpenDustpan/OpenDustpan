package jobstarter_test

import (
	"context"
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/graph"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/trigger"
	"opendustpan.org/opendustpan/pkg/trigger/jobstarter"
)

type fakeJobs struct {
	actor job.Actor
	req   job.StartRequest
	calls int
	err   error
}

func (f *fakeJobs) Start(_ context.Context, a job.Actor, req job.StartRequest) (job.Job, error) {
	f.calls++
	f.actor, f.req = a, req

	if f.err != nil {
		return job.Job{}, f.err
	}

	return job.Job{ID: "job-42"}, nil //nolint:exhaustruct // Only the ID matters to this test.
}

type fakeUnits struct{ err error }

func (u fakeUnits) Object(_ context.Context, unit graph.Unit) (identity.Object, error) {
	if u.err != nil {
		return identity.Object{}, u.err
	}

	obj, err := identity.UnitObject(unit.Tool, unit.Namespace, "name-of-"+unit.ID)
	if err != nil {
		return identity.Object{}, err //nolint:wrapcheck // A test double.
	}

	return obj.WithUnitID(unit.ID), nil
}

func (fakeUnits) Find(context.Context, namespace.Name, string) (graph.Unit, error) {
	return graph.Unit{}, errors.New("not used")
}

var unit = graph.Unit{Namespace: "acme", Tool: "opentofu", ID: "u1"}

func plan(source job.SourceKind) trigger.PlanRequest {
	return trigger.PlanRequest{
		Trigger: "trg-1", Kind: trigger.KindSchedule, IdempotencyKey: "trigger:trg-1:schedule:1",
		Reason: "trigger trg-1 (schedule): schedule slot", Source: source, Saver: "user:ada",
	}
}

func TestNewNeedsPorts(t *testing.T) {
	t.Parallel()

	if _, err := jobstarter.New(nil, fakeUnits{}); !errors.Is(err, trigger.ErrInvalid) {
		t.Errorf("New with no jobs = %v, want ErrInvalid", err)
	}

	if _, err := jobstarter.New(&fakeJobs{}, nil); !errors.Is(err, trigger.ErrInvalid) {
		t.Errorf("New with no units = %v, want ErrInvalid", err)
	}
}

// TestStartPlanRequest checks that every start is a plan on a runner, with the
// settings of the unit, and nothing that a trigger must not set.
func TestStartPlanRequest(t *testing.T) {
	t.Parallel()

	actor := job.Actor{
		Principal: identity.Principal{Subject: "system:namespace-service:acme"},
	} //nolint:exhaustruct // A test value.

	for _, source := range []job.SourceKind{"", job.SourceSchedule, job.SourceForge, job.SourceTrigger} {
		jobs := &fakeJobs{}

		s, err := jobstarter.New(jobs, fakeUnits{})
		if err != nil {
			t.Fatalf("New = %v", err)
		}

		id, err := s.StartPlan(t.Context(), actor, unit, plan(source))
		if err != nil || id != "job-42" {
			t.Fatalf("StartPlan(%q) = %q, %v; want job-42", source, id, err)
		}

		req := jobs.req

		if req.Operation != job.OperationPlan || req.Executor != job.ExecutorRunner {
			t.Errorf(
				"source %q: operation %q on executor %q, want a plan on a runner",
				source,
				req.Operation,
				req.Executor,
			)
		}

		if req.Unit.UnitID() != "u1" || req.Unit.UnitName() != "name-of-u1" || req.Source != source ||
			req.IdempotencyKey != "trigger:trg-1:schedule:1" || req.Reason == "" {
			t.Errorf("source %q: request = %+v", source, req)
		}

		if req.Trigger == nil || req.Trigger.ID != "trg-1" || req.Trigger.Saver != "user:ada" {
			t.Errorf("source %q: trigger = %+v, want trg-1 saved by user:ada", source, req.Trigger)
		}

		// Nothing that decides what runs or where comes from the trigger.
		if req.BreakGlass != nil || len(req.Edits) != 0 || req.Delivery != "" || req.Pool != "" ||
			req.Location != (job.Location{}) {
			t.Errorf("source %q: request = %+v, want no break-glass, edits, delivery, pool or location", source, req)
		}

		if jobs.actor.Principal.Subject != actor.Principal.Subject {
			t.Errorf("source %q: actor = %q, want the actor of the call", source, jobs.actor.Principal.Subject)
		}
	}
}

func TestStartPlanErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		jobs  *fakeJobs
		units fakeUnits
		req   trigger.PlanRequest
		want  error
		calls int
	}{
		{"a unit that is gone", &fakeJobs{}, fakeUnits{err: trigger.ErrUnitGone}, plan(""), job.ErrNotFound, 0},
		{"a lookup that fails", &fakeJobs{}, fakeUnits{err: errors.New("down")}, plan(""), nil, 0},
		{
			"a trigger ID that is not valid",
			&fakeJobs{},
			fakeUnits{},
			trigger.PlanRequest{
				Trigger:        "No Way",
				Kind:           trigger.KindSchedule,
				Reason:         "x",
				Source:         "",
				Saver:          "s",
				IdempotencyKey: "",
			},
			trigger.ErrInvalid,
			0,
		},
		{"a freeze", &fakeJobs{err: job.ErrFrozen}, fakeUnits{}, plan(""), job.ErrFrozen, 1},
		{"a policy", &fakeJobs{err: job.ErrPolicyDenied}, fakeUnits{}, plan(""), job.ErrPolicyDenied, 1},
		{"a limit", &fakeJobs{err: job.ErrLimit}, fakeUnits{}, plan(""), job.ErrLimit, 1},
		{"an unsupported operation", &fakeJobs{err: job.ErrUnsupported}, fakeUnits{}, plan(""), job.ErrUnsupported, 1},
		{"a denied action", &fakeJobs{err: identity.ErrDenied}, fakeUnits{}, plan(""), identity.ErrDenied, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s, err := jobstarter.New(tt.jobs, tt.units)
			if err != nil {
				t.Fatalf("New = %v", err)
			}

			id, err := s.StartPlan(t.Context(), job.Actor{}, unit, tt.req) //nolint:exhaustruct // A test value.
			if err == nil || id != "" {
				t.Fatalf("StartPlan = %q, %v; want an error and no job", id, err)
			}

			if tt.want != nil && !errors.Is(err, tt.want) {
				t.Errorf("StartPlan = %v, want %v", err, tt.want)
			}

			if tt.jobs.calls != tt.calls {
				t.Errorf("job service calls = %d, want %d", tt.jobs.calls, tt.calls)
			}
		})
	}
}

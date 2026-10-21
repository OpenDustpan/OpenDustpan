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

func newJobs(err error) *fakeJobs {
	return &fakeJobs{actor: noActor(), req: noRequest(), calls: 0, err: err}
}

// noActor returns an actor with no field set.
func noActor() job.Actor {
	var a job.Actor

	return a
}

// noRequest returns a job request with no field set.
func noRequest() job.StartRequest {
	var r job.StartRequest

	return r
}

// noLocation returns a location with no field set.
func noLocation() job.Location {
	var l job.Location

	return l
}

func (f *fakeJobs) Start(_ context.Context, a job.Actor, req job.StartRequest) (job.Job, error) {
	f.calls++
	f.actor, f.req = a, req

	if f.err != nil {
		var none job.Job

		return none, f.err
	}

	// Only the ID matters to this test.
	var j job.Job

	j.ID = "job-42"

	return j, nil
}

type fakeUnits struct{ err error }

func newUnits(err error) fakeUnits { return fakeUnits{err: err} }

func (u fakeUnits) Object(_ context.Context, unit graph.Unit) (identity.Object, error) {
	if u.err != nil {
		return identity.Object{}, u.err
	}

	obj, err := identity.UnitObject(unit.Tool, unit.Namespace, "name-of-"+unit.ID)
	if err != nil {
		return identity.Object{}, err
	}

	return obj.WithUnitID(unit.ID), nil
}

func (fakeUnits) Find(context.Context, namespace.Name, string) (graph.Unit, error) {
	return graph.Unit{}, errNotUsed
}

var errNotUsed = errors.New("not used")

// errDown is the error of a lookup that fails.
var errDown = errors.New("down")

func testUnit() graph.Unit { return graph.Unit{Namespace: "acme", Tool: "opentofu", ID: "u1"} }

func plan(source job.SourceKind) trigger.PlanRequest {
	return trigger.PlanRequest{
		Trigger: "trg-1", Kind: trigger.KindSchedule, IdempotencyKey: "trigger:trg-1:schedule:1",
		Reason: "trigger trg-1 (schedule): schedule slot", Source: source, Saver: "user:ada",
		Location: noLocation(), Request: noInfo(),
	}
}

// noInfo returns request facts with no field set.
func noInfo() trigger.RequestInfo {
	var i trigger.RequestInfo

	return i
}

func TestNewNeedsPorts(t *testing.T) {
	t.Parallel()

	if _, err := jobstarter.New(nil, newUnits(nil)); !errors.Is(err, trigger.ErrInvalid) {
		t.Errorf("New with no jobs = %v, want ErrInvalid", err)
	}

	if _, err := jobstarter.New(newJobs(nil), nil); !errors.Is(err, trigger.ErrInvalid) {
		t.Errorf("New with no units = %v, want ErrInvalid", err)
	}
}

// TestStartPlanRequest checks that every start is a plan on a runner, with the
// settings of the unit, and nothing that a trigger must not set.
func TestStartPlanRequest(t *testing.T) {
	t.Parallel()

	var principal identity.Principal

	principal.Subject = "system:namespace-service:acme"

	actor := noActor()
	actor.Principal = principal

	for _, source := range []job.SourceKind{"", job.SourceSchedule, job.SourceForge, job.SourceTrigger} {
		jobs := newJobs(nil)

		s, err := jobstarter.New(jobs, newUnits(nil))
		if err != nil {
			t.Fatalf("New = %v", err)
		}

		id, err := s.StartPlan(t.Context(), actor, testUnit(), plan(source))
		if err != nil || id != "job-42" {
			t.Fatalf("StartPlan(%q) = %q, %v; want job-42", source, id, err)
		}

		checkPlanRequest(t, source, jobs.req)

		if jobs.actor.Principal.Subject != actor.Principal.Subject {
			t.Errorf("source %q: actor = %q, want the actor of the call", source, jobs.actor.Principal.Subject)
		}
	}
}

// checkNoDecisions checks that the request has nothing that decides what runs
// or where.
func checkNoDecisions(t *testing.T, source job.SourceKind, req job.StartRequest) {
	t.Helper()

	if req.BreakGlass != nil || len(req.Edits) != 0 || req.Delivery != "" || req.Pool != "" ||
		req.Location != noLocation() {
		t.Errorf("source %q: request = %+v, want no break-glass, edits, delivery, pool or location", source, req)
	}
}

// checkPlanRequest checks the job request of a start with the source kind source.
func checkPlanRequest(t *testing.T, source job.SourceKind, req job.StartRequest) {
	t.Helper()

	if req.Operation != job.OperationPlan || req.Executor != job.ExecutorRunner {
		t.Errorf("source %q: operation %q on executor %q, want a plan on a runner", source, req.Operation, req.Executor)
	}

	if req.Unit.UnitID() != "u1" || req.Unit.UnitName() != "name-of-u1" || req.Source != source {
		t.Errorf("source %q: unit %q (%q) and source %q", source, req.Unit.UnitID(), req.Unit.UnitName(), req.Source)
	}

	if req.IdempotencyKey != "trigger:trg-1:schedule:1" || req.Reason == "" {
		t.Errorf("source %q: request = %+v", source, req)
	}

	if req.Trigger == nil || req.Trigger.ID != "trg-1" || req.Trigger.Saver != "user:ada" {
		t.Errorf("source %q: trigger = %+v, want trg-1 saved by user:ada", source, req.Trigger)
	}

	checkNoDecisions(t, source, req)
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
		{"a unit that is gone", newJobs(nil), newUnits(trigger.ErrUnitGone), plan(""), job.ErrNotFound, 0},
		{"a lookup that fails", newJobs(nil), newUnits(errDown), plan(""), nil, 0},
		{
			"a trigger ID that is not valid",
			newJobs(nil),
			newUnits(nil),
			trigger.PlanRequest{
				Trigger:        "No Way",
				Kind:           trigger.KindSchedule,
				Reason:         "x",
				Source:         "",
				Saver:          "s",
				IdempotencyKey: "",
				Location:       noLocation(),
				Request:        noInfo(),
			},
			trigger.ErrInvalid,
			0,
		},
		{"a freeze", newJobs(job.ErrFrozen), newUnits(nil), plan(""), job.ErrFrozen, 1},
		{"a policy", newJobs(job.ErrPolicyDenied), newUnits(nil), plan(""), job.ErrPolicyDenied, 1},
		{"a limit", newJobs(job.ErrLimit), newUnits(nil), plan(""), job.ErrLimit, 1},
		{"an unsupported operation", newJobs(job.ErrUnsupported), newUnits(nil), plan(""), job.ErrUnsupported, 1},
		{"a denied action", newJobs(identity.ErrDenied), newUnits(nil), plan(""), identity.ErrDenied, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s, err := jobstarter.New(tt.jobs, tt.units)
			if err != nil {
				t.Fatalf("New = %v", err)
			}

			id, err := s.StartPlan(t.Context(), noActor(), testUnit(), tt.req)
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

// TestStartPlanRequestFacts proves the facts of the request of a manual fire
// reach the job request, for the audit entry of the start, and that a start
// that no person makes carries none.
func TestStartPlanRequestFacts(t *testing.T) {
	t.Parallel()

	jobs := newJobs(nil)

	s, err := jobstarter.New(jobs, newUnits(nil))
	if err != nil {
		t.Fatalf("New = %v", err)
	}

	req := plan("")
	req.Request = trigger.RequestInfo{
		Channel: "api", SourceIP: "192.0.2.7", UserAgent: "probe-agent", RequestID: "req-1",
	}

	if _, err := s.StartPlan(t.Context(), noActor(), testUnit(), req); err != nil {
		t.Fatalf("StartPlan = %v", err)
	}

	got := jobs.req
	if got.Channel != "api" || got.SourceIP != "192.0.2.7" || got.UserAgent != "probe-agent" ||
		got.RequestID != "req-1" {
		t.Errorf("job request = %+v, want the facts of the request", got)
	}

	if _, err := s.StartPlan(t.Context(), noActor(), testUnit(), plan(job.SourceTrigger)); err != nil {
		t.Fatalf("StartPlan = %v", err)
	}

	got = jobs.req
	if got.Channel != "" || got.SourceIP != "" || got.UserAgent != "" || got.RequestID != "" {
		t.Errorf("job request = %+v, want no facts for a start that no person makes", got)
	}
}

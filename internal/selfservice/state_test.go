package selfservice_test

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/quota"
	"opendustpan.org/opendustpan/internal/selfservice"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
)

// delivered returns a delivery of d, merged after mergedAfter, or with no
// merge for 0.
func delivered(d job.Delivery, mergedAfter time.Duration) *job.DeliveryResult {
	r := &job.DeliveryResult{
		Delivery: d, Commit: "c0ffee", Branch: "dustpan/job-1", ReviewRequest: "",
		MergedAt: time.Time{}, DeliveredAt: time.Unix(1_790_000_000, 0),
	}
	if mergedAfter > 0 {
		r.MergedAt = r.DeliveredAt.Add(mergedAfter)
	}

	return r
}

func TestStateOf(t *testing.T) {
	t.Parallel()

	cases := []struct {
		state    job.State
		delivery *job.DeliveryResult
		want     selfservice.RevisionState
	}{
		{state: job.StateQueued, delivery: nil, want: selfservice.StateQueued},
		{state: job.StateAwaitingApproval, delivery: nil, want: selfservice.StateAwaitingApproval},
		{state: job.StateDispatched, delivery: nil, want: selfservice.StateRunning},
		{state: job.StateRunning, delivery: nil, want: selfservice.StateRunning},
		{state: job.StateCanceling, delivery: nil, want: selfservice.StateRunning},
		{
			state:    job.StateAwaitingState,
			delivery: delivered(job.DeliveryReviewRequest, 0),
			want:     selfservice.StateAwaitingMerge,
		},
		{
			state:    job.StateAwaitingState,
			delivery: delivered(job.DeliveryReviewRequest, time.Hour),
			want:     selfservice.StateAwaitingState,
		},
		{
			state:    job.StateAwaitingState,
			delivery: delivered(job.DeliveryDirectPush, 0),
			want:     selfservice.StateAwaitingState,
		},
		{state: job.StateSucceeded, delivery: nil, want: selfservice.StateApplied},
		{state: job.StateFailed, delivery: nil, want: selfservice.StateFailed},
		{state: job.StateRejected, delivery: nil, want: selfservice.StateRejected},
		{state: job.StateCanceled, delivery: nil, want: selfservice.StateCanceled},
		{state: job.StateExpired, delivery: nil, want: selfservice.StateExpired},
	}

	for _, tc := range cases {
		var j job.Job

		j.State, j.Delivery = tc.state, tc.delivery

		got, err := selfservice.StateOf(&j)
		if err != nil || got != tc.want {
			t.Fatalf("StateOf(%s) = %q, %v, want %q", tc.state, got, err, tc.want)
		}

		if got.Terminal() != tc.state.Terminal() {
			t.Fatalf("StateOf(%s) = %q: terminal %v, the job is terminal %v",
				tc.state, got, got.Terminal(), tc.state.Terminal())
		}
	}

	var paused job.Job

	paused.State = "paused"
	if _, err := selfservice.StateOf(&paused); !errors.Is(err, selfservice.ErrInvalid) {
		t.Fatalf("StateOf(paused) = %v, want ErrInvalid", err)
	}
}

func TestCheckTransition(t *testing.T) {
	t.Parallel()

	cases := []struct {
		from, to selfservice.RevisionState
		want     error
	}{
		{from: selfservice.StateAccepted, to: selfservice.StateQueued, want: nil},
		{from: selfservice.StateAccepted, to: selfservice.StateRejected, want: nil},
		{from: selfservice.StateQueued, to: selfservice.StateAwaitingApproval, want: nil},
		{from: selfservice.StateAwaitingApproval, to: selfservice.StateQueued, want: nil},
		{from: selfservice.StateRunning, to: selfservice.StateAwaitingMerge, want: nil},
		{from: selfservice.StateAwaitingMerge, to: selfservice.StateAwaitingState, want: nil},
		{from: selfservice.StateAwaitingState, to: selfservice.StateApplied, want: nil},
		{from: selfservice.StateAwaitingMerge, to: selfservice.StateSuperseded, want: nil},
		{from: selfservice.StateAwaitingState, to: selfservice.StateSuperseded, want: nil},
		{from: selfservice.StateRunning, to: selfservice.StateRunning, want: nil},
		{from: selfservice.StateApplied, to: selfservice.StateApplied, want: nil},
		{from: selfservice.StateRunning, to: selfservice.StateSuperseded, want: nil},
		{from: selfservice.StateAccepted, to: selfservice.StateSuperseded, want: nil},
		{from: selfservice.StateQueued, to: selfservice.StateAccepted, want: selfservice.ErrTransition},
		{from: selfservice.StateApplied, to: selfservice.StateRunning, want: selfservice.ErrFinished},
		{from: selfservice.StateSuperseded, to: selfservice.StateApplied, want: selfservice.ErrFinished},
		{from: "lost", to: selfservice.StateQueued, want: selfservice.ErrInvalid},
	}

	for _, tc := range cases {
		err := selfservice.CheckTransition(tc.from, tc.to)
		if !errors.Is(err, tc.want) || (tc.want == nil && err != nil) {
			t.Fatalf("CheckTransition(%s, %s) = %v, want %v", tc.from, tc.to, err, tc.want)
		}
	}
}

func TestCheckSupersede(t *testing.T) {
	t.Parallel()

	rev := func(state selfservice.RevisionState, j job.ID) *selfservice.Revision {
		var r selfservice.Revision

		r.State, r.Job, r.Number = state, j, 1

		return &r
	}
	withDelivery := func(d *job.DeliveryResult) *job.Job {
		var j job.Job

		j.ID, j.State, j.Delivery = "job-1", job.StateAwaitingState, d

		return &j
	}

	cases := []struct {
		name string
		old  *selfservice.Revision
		job  *job.Job
		want error
	}{
		{"accepted with no job", rev(selfservice.StateAccepted, ""), nil, nil},
		{"queued", rev(selfservice.StateQueued, "job-1"), nil, nil},
		{
			"a review request with no merge", rev(selfservice.StateAwaitingMerge, "job-1"),
			withDelivery(delivered(job.DeliveryReviewRequest, 0)), nil,
		},
		{"a revision from git that waits for its state", rev(selfservice.StateAwaitingState, ""), nil, nil},
		{"a job in git for good", rev(selfservice.StateAwaitingState, "job-1"), nil, selfservice.ErrConflict},
		{
			"a merge that the revision did not see yet", rev(selfservice.StateAwaitingMerge, "job-1"),
			withDelivery(delivered(job.DeliveryReviewRequest, time.Hour)), selfservice.ErrConflict,
		},
		{
			"a direct push during a run", rev(selfservice.StateRunning, "job-1"),
			withDelivery(delivered(job.DeliveryDirectPush, 0)), selfservice.ErrConflict,
		},
		{"an ended revision", rev(selfservice.StateApplied, "job-1"), nil, selfservice.ErrFinished},
	}

	for _, tc := range cases {
		err := selfservice.CheckSupersede(tc.old, tc.job)
		if !errors.Is(err, tc.want) || (tc.want == nil && err != nil) {
			t.Fatalf("%s: CheckSupersede() = %v, want %v", tc.name, err, tc.want)
		}
	}
}

func TestRevisionDeadline(t *testing.T) {
	t.Parallel()

	at := time.Unix(1_790_000_000, 0).UTC()
	day := 24 * time.Hour

	if got := selfservice.RevisionDeadline(selfservice.OriginGit, at); !got.Equal(at.Add(15 * day)) {
		t.Fatalf("RevisionDeadline(git) = %v, want 14 days of state wait and 1 day of grace", got)
	}

	if got := selfservice.RevisionDeadline(selfservice.OriginAPI, at); !got.Equal(at.Add(31 * day)) {
		t.Fatalf("RevisionDeadline(api) = %v, want 30 days of job limits and 1 day of grace", got)
	}

	var r selfservice.Revision

	r.State, r.Deadline = selfservice.StateAwaitingState, selfservice.RevisionDeadline(selfservice.OriginGit, at)

	if r.Expired(r.Deadline.Add(-time.Second)) || !r.Expired(r.Deadline) {
		t.Fatal("Expired() does not end the revision at its deadline")
	}

	r.State = selfservice.StateApplied
	if r.Expired(r.Deadline.Add(day)) {
		t.Fatal("Expired() of an ended revision")
	}
}

func TestExpiryOf(t *testing.T) {
	t.Parallel()

	deadline := time.Unix(1_790_000_000, 0).UTC()
	after := deadline.Add(time.Minute)

	jobIn := func(s job.State) *job.Job {
		var j job.Job

		j.ID, j.State = "job-1", s

		return &j
	}

	tests := []struct {
		name     string
		job      job.ID
		j        *job.Job
		now      time.Time
		end      selfservice.RevisionState
		deadline time.Time
		err      error
	}{
		{
			name: "before the lead", job: "job-1", j: jobIn(job.StateRunning),
			now: deadline.Add(-selfservice.ExtendLead - time.Minute), end: "", deadline: time.Time{}, err: nil,
		},
		{
			name: "a running job moves the deadline one hour before it", job: "job-1", j: jobIn(job.StateRunning),
			now: deadline.Add(-time.Hour), end: "", deadline: deadline.Add(quota.ReservationGrace), err: nil,
		},
		{
			name: "no job does not expire before the deadline", job: "", j: nil, now: deadline.Add(-time.Hour),
			end: "", deadline: time.Time{}, err: nil,
		},
		{
			name: "a job that ended does not end the revision before the deadline", job: "job-1",
			j: jobIn(job.StateSucceeded), now: deadline.Add(-time.Hour),
			end: "", deadline: time.Time{}, err: nil,
		},
		{
			name: "no job expires", job: "", j: nil, now: after,
			end: selfservice.StateExpired, deadline: time.Time{}, err: nil,
		},
		{
			name: "a running job moves the deadline", job: "job-1", j: jobIn(job.StateRunning), now: after,
			end: "", deadline: deadline.Add(quota.ReservationGrace), err: nil,
		},
		{
			name: "a job at a gate moves the deadline", job: "job-1", j: jobIn(job.StateAwaitingApproval), now: after,
			end: "", deadline: deadline.Add(quota.ReservationGrace), err: nil,
		},
		{
			name: "a job that succeeded applies", job: "job-1", j: jobIn(job.StateSucceeded), now: after,
			end: selfservice.StateApplied, deadline: time.Time{}, err: nil,
		},
		{
			name: "a job that was canceled cancels", job: "job-1", j: jobIn(job.StateCanceled), now: after,
			end: selfservice.StateCanceled, deadline: time.Time{}, err: nil,
		},
		{
			name: "a job that the caller did not read", job: "job-1", j: nil, now: after,
			end: "", deadline: time.Time{}, err: selfservice.ErrInvalid,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var r selfservice.Revision

			r.State, r.Job, r.Deadline = selfservice.StateRunning, tt.job, deadline

			got, err := selfservice.ExpiryOf(&r, tt.j, tt.now)
			if !errors.Is(err, tt.err) {
				t.Fatalf("ExpiryOf() error = %v, want %v", err, tt.err)
			}

			if got.End != tt.end || !got.Deadline.Equal(tt.deadline) {
				t.Fatalf("ExpiryOf() = %+v, want end %q and deadline %v", got, tt.end, tt.deadline)
			}
		})
	}
}

var errDatabase = errors.New("database is not available")

func TestStartFailure(t *testing.T) {
	t.Parallel()

	cases := []struct {
		err   error
		want  selfservice.RevisionState
		final bool
	}{
		{err: job.ErrPolicyDenied, want: selfservice.StateRejected, final: true},
		{err: fmt.Errorf("start: %w", job.ErrFrozen), want: selfservice.StateRejected, final: true},
		{err: job.ErrAgentDenied, want: selfservice.StateRejected, final: true},
		{err: job.ErrLimit, want: selfservice.StateFailed, final: true},
		{err: job.ErrInvalid, want: selfservice.StateFailed, final: true},
		{err: job.ErrUnsupported, want: selfservice.StateFailed, final: true},
		{err: errDatabase, want: "", final: false},
	}

	for _, tc := range cases {
		got, final := selfservice.StartFailure(tc.err)
		if got != tc.want || final != tc.final {
			t.Fatalf("StartFailure(%v) = %q, %v, want %q, %v", tc.err, got, final, tc.want, tc.final)
		}
	}
}

func TestGitPath(t *testing.T) {
	t.Parallel()

	ref := selfservice.RequestRef{Namespace: "payments", Blueprint: "postgres", Name: "orders-db"}
	if got := selfservice.GitPath(ref); got != "self-service/payments/postgres/orders-db/request.json" {
		t.Fatalf("GitPath() = %q", got)
	}

	back, err := selfservice.ParseGitPath(selfservice.GitPath(ref))
	if err != nil || back != ref {
		t.Fatalf("ParseGitPath(GitPath()) = %+v, %v", back, err)
	}

	for _, bad := range []string{
		"self-service/payments/postgres/orders-db/request.yaml",
		"infra/payments/postgres/orders-db/request.json",
		"self-service/payments/postgres/request.json",
		"self-service/payments/postgres/Orders/request.json",
		"self-service/pay ments/postgres/orders-db/request.json",
		"self-service/payments/postgres/orders-db/x/request.json",
	} {
		if _, err := selfservice.ParseGitPath(bad); !errors.Is(err, selfservice.ErrInvalid) {
			t.Fatalf("ParseGitPath(%q) = %v, want ErrInvalid", bad, err)
		}
	}

	d := parseDocument(t, string(readFile(t, "orders-db.request.json")))
	if err := selfservice.CheckPath(selfservice.GitPath(ref), &d); err != nil {
		t.Fatalf("CheckPath() = %v", err)
	}

	other := selfservice.RequestRef{Namespace: "payments", Blueprint: "postgres", Name: "users-db"}
	if err := selfservice.CheckPath(selfservice.GitPath(other), &d); !errors.Is(err, selfservice.ErrPathMismatch) {
		t.Fatalf("CheckPath(another name) = %v, want ErrPathMismatch", err)
	}
}

func TestRequirements(t *testing.T) {
	t.Parallel()

	u := unit(t, "databases/orders-db", "ws-1")
	bp := selfservice.Object{
		Unit:      identity.Object{},
		Kind:      selfservice.ObjectBlueprint,
		Namespace: "payments",
		Blueprint: "postgres",
	}
	ns := selfservice.Object{
		Unit:      identity.Object{},
		Kind:      selfservice.ObjectNamespace,
		Namespace: "payments",
		Blueprint: "",
	}
	on := selfservice.Object{Unit: u, Kind: selfservice.ObjectUnit, Namespace: "payments", Blueprint: "postgres"}

	target := func(op selfservice.Operation, unit identity.Object, breakGlass bool) selfservice.Target {
		return selfservice.Target{
			Unit:       unit,
			Namespace:  "payments",
			Blueprint:  "postgres",
			Operation:  op,
			BreakGlass: breakGlass,
		}
	}

	cases := []struct {
		name string
		op   selfservice.Op
		t    selfservice.Target
		want []selfservice.Requirement
		err  error
	}{
		{
			"write draft", selfservice.OpWriteDraft, target("", identity.Object{}, false),
			[]selfservice.Requirement{{Action: selfservice.ActionBlueprintPublish, Object: bp}},
			nil,
		},
		{
			"publish", selfservice.OpPublish, target("", identity.Object{}, false),
			[]selfservice.Requirement{{Action: selfservice.ActionBlueprintPublish, Object: bp}},
			nil,
		},
		{
			"create", selfservice.OpSubmit, target(selfservice.OperationCreate, identity.Object{}, false),
			[]selfservice.Requirement{{Action: selfservice.ActionSelfServiceRequest, Object: bp}},
			nil,
		},
		{
			"update", selfservice.OpSubmit, target(selfservice.OperationUpdate, u, false),
			[]selfservice.Requirement{{Action: selfservice.ActionUnitEditInputs, Object: on}},
			nil,
		},
		{
			"destroy with break-glass", selfservice.OpSubmit, target(selfservice.OperationDestroy, u, true),
			[]selfservice.Requirement{
				{Action: selfservice.ActionUnitRequestDestroy, Object: on},
				{Action: job.ActionNamespaceBreakFreeze, Object: ns},
			},
			nil,
		},
		{
			"an update with no unit",
			selfservice.OpSubmit,
			target(selfservice.OperationUpdate, identity.Object{}, false),
			nil,
			selfservice.ErrInvalid,
		},
		{
			"options of a create", selfservice.OpOptions, target(selfservice.OperationCreate, identity.Object{}, false),
			[]selfservice.Requirement{{Action: selfservice.ActionSelfServiceRequest, Object: bp}},
			nil,
		},
		{
			"options of an update", selfservice.OpOptions, target(selfservice.OperationUpdate, u, false),
			[]selfservice.Requirement{{Action: selfservice.ActionUnitEditInputs, Object: on}},
			nil,
		},
		{
			"check of an update", selfservice.OpCheck, target(selfservice.OperationUpdate, u, true),
			[]selfservice.Requirement{{Action: selfservice.ActionUnitEditInputs, Object: on}},
			nil,
		},
		{
			"check of a destroy", selfservice.OpCheck, target(selfservice.OperationDestroy, u, false),
			[]selfservice.Requirement{{Action: selfservice.ActionUnitRequestDestroy, Object: on}},
			nil,
		},
		{"options with no operation", selfservice.OpOptions, target("", u, false), nil, selfservice.ErrInvalid},
		{
			"cancel", selfservice.OpCancel, target("", u, false),
			[]selfservice.Requirement{{Action: job.ActionJobCancel, Object: on}},
			nil,
		},
		{
			"render of a create", selfservice.OpRender, target(selfservice.OperationCreate, identity.Object{}, false),
			[]selfservice.Requirement{
				{Action: selfservice.ActionSelfServiceRender, Object: bp},
				{Action: selfservice.ActionSelfServiceRequest, Object: bp},
			},
			nil,
		},
		{
			"render of an update", selfservice.OpRender, target(selfservice.OperationUpdate, u, false),
			[]selfservice.Requirement{
				{Action: selfservice.ActionSelfServiceRender, Object: on},
				{Action: selfservice.ActionUnitEditInputs, Object: on},
			},
			nil,
		},
		{
			"render of a destroy", selfservice.OpRender, target(selfservice.OperationDestroy, u, false),
			[]selfservice.Requirement{
				{Action: selfservice.ActionSelfServiceRender, Object: on},
				{Action: selfservice.ActionUnitRequestDestroy, Object: on},
			},
			nil,
		},
		{
			"render that stores no revision", selfservice.OpRender, target(selfservice.OperationNone, u, false),
			[]selfservice.Requirement{{Action: selfservice.ActionSelfServiceRender, Object: on}},
			nil,
		},
		{
			"a render of an update with no unit", selfservice.OpRender,
			target(selfservice.OperationUpdate, identity.Object{}, false), nil, selfservice.ErrInvalid,
		},
		{
			"list requests", selfservice.OpListRequests, target("", identity.Object{}, false),
			[]selfservice.Requirement{{Action: selfservice.ActionSelfServiceRead, Object: ns}},
			nil,
		},
		{
			"manage policy", selfservice.OpManagePolicy, target("", identity.Object{}, false),
			[]selfservice.Requirement{{Action: selfservice.ActionNamespaceManagePolicy, Object: ns}},
			nil,
		},
		{"an unknown operation", "approve", target("", u, false), nil, selfservice.ErrInvalid},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := selfservice.Requirements(tc.op, tc.t)
			if !errors.Is(err, tc.err) || (tc.err == nil && err != nil) || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Requirements() = %+v, %v, want %+v, %v", got, err, tc.want, tc.err)
			}
		})
	}
}

func TestPublishRequirements(t *testing.T) {
	t.Parallel()

	b := parseBlueprint(t, readFile(t, "postgres.blueprint.json"))
	got := selfservice.PublishRequirements("payments", &b)

	// The publisher needs the publish action, and can attach the source
	// principal only when it can already act as it. It needs no grant on
	// the sources.
	if len(got) != 2 || got[0].Action != selfservice.ActionBlueprintPublish ||
		got[1].Action != identity.ActionPrincipalManageTokens || got[1].Object.Kind != selfservice.ObjectPrincipal {
		t.Fatalf("PublishRequirements() = %+v", got)
	}

	if o, ok := got[1].Object.Identity(); !ok || o.PrincipalID() != "self-service-reader" {
		t.Fatalf("Identity() of the source principal = %v, %v", o, ok)
	}

	if _, ok := got[0].Object.Identity(); ok {
		t.Fatal("a blueprint has an identity object")
	}

	b.Inputs = b.Inputs[:1]
	b.SourcePrincipal = ""

	if got := selfservice.PublishRequirements("payments", &b); len(got) != 1 {
		t.Fatalf("PublishRequirements(no source) = %+v, want the publish action only", got)
	}
}

func TestSourceRequirements(t *testing.T) {
	t.Parallel()

	b := parseBlueprint(t, readFile(t, "postgres.blueprint.json"))

	// Two sources of blueprints need unit:read on the namespace once.
	got := selfservice.SourceRequirements("payments", &b)
	if len(got) != 1 || got[0].Action != identity.ActionUnitRead || got[0].Object.Kind != selfservice.ObjectNamespace {
		t.Fatalf("SourceRequirements() = %+v", got)
	}

	b.Inputs[1].Choice.Source.Blueprint = ""
	b.Inputs[1].Choice.Source.Collection = "networks"

	got = selfservice.SourceRequirements("payments", &b)
	if len(got) != 2 || got[0].Object.Kind != selfservice.ObjectCollection ||
		got[1].Object.Kind != selfservice.ObjectNamespace {
		t.Fatalf("SourceRequirements(a collection) = %+v", got)
	}
}

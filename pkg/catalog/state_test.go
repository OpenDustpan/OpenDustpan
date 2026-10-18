package catalog_test

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/catalog"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/quota"
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
		want     catalog.RevisionState
	}{
		{state: job.StateQueued, delivery: nil, want: catalog.StateQueued},
		{state: job.StateAwaitingApproval, delivery: nil, want: catalog.StateAwaitingApproval},
		{state: job.StateDispatched, delivery: nil, want: catalog.StateRunning},
		{state: job.StateRunning, delivery: nil, want: catalog.StateRunning},
		{state: job.StateCanceling, delivery: nil, want: catalog.StateRunning},
		{
			state:    job.StateAwaitingState,
			delivery: delivered(job.DeliveryReviewRequest, 0),
			want:     catalog.StateAwaitingMerge,
		},
		{
			state:    job.StateAwaitingState,
			delivery: delivered(job.DeliveryReviewRequest, time.Hour),
			want:     catalog.StateAwaitingState,
		},
		{
			state:    job.StateAwaitingState,
			delivery: delivered(job.DeliveryDirectPush, 0),
			want:     catalog.StateAwaitingState,
		},
		{state: job.StateSucceeded, delivery: nil, want: catalog.StateApplied},
		{state: job.StateFailed, delivery: nil, want: catalog.StateFailed},
		{state: job.StateRejected, delivery: nil, want: catalog.StateRejected},
		{state: job.StateCanceled, delivery: nil, want: catalog.StateCanceled},
		{state: job.StateExpired, delivery: nil, want: catalog.StateExpired},
	}

	for _, tc := range cases {
		var j job.Job

		j.State, j.Delivery = tc.state, tc.delivery

		got, err := catalog.StateOf(&j)
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
	if _, err := catalog.StateOf(&paused); !errors.Is(err, catalog.ErrInvalid) {
		t.Fatalf("StateOf(paused) = %v, want ErrInvalid", err)
	}
}

func TestCheckTransition(t *testing.T) {
	t.Parallel()

	cases := []struct {
		from, to catalog.RevisionState
		want     error
	}{
		{from: catalog.StateAccepted, to: catalog.StateQueued, want: nil},
		{from: catalog.StateAccepted, to: catalog.StateRejected, want: nil},
		{from: catalog.StateQueued, to: catalog.StateAwaitingApproval, want: nil},
		{from: catalog.StateAwaitingApproval, to: catalog.StateQueued, want: nil},
		{from: catalog.StateRunning, to: catalog.StateAwaitingMerge, want: nil},
		{from: catalog.StateAwaitingMerge, to: catalog.StateAwaitingState, want: nil},
		{from: catalog.StateAwaitingState, to: catalog.StateApplied, want: nil},
		{from: catalog.StateAwaitingMerge, to: catalog.StateSuperseded, want: nil},
		{from: catalog.StateAwaitingState, to: catalog.StateSuperseded, want: nil},
		{from: catalog.StateRunning, to: catalog.StateRunning, want: nil},
		{from: catalog.StateApplied, to: catalog.StateApplied, want: nil},
		{from: catalog.StateRunning, to: catalog.StateSuperseded, want: nil},
		{from: catalog.StateAccepted, to: catalog.StateSuperseded, want: nil},
		{from: catalog.StateQueued, to: catalog.StateAccepted, want: catalog.ErrTransition},
		{from: catalog.StateApplied, to: catalog.StateRunning, want: catalog.ErrFinished},
		{from: catalog.StateSuperseded, to: catalog.StateApplied, want: catalog.ErrFinished},
		{from: "lost", to: catalog.StateQueued, want: catalog.ErrInvalid},
	}

	for _, tc := range cases {
		err := catalog.CheckTransition(tc.from, tc.to)
		if !errors.Is(err, tc.want) || (tc.want == nil && err != nil) {
			t.Fatalf("CheckTransition(%s, %s) = %v, want %v", tc.from, tc.to, err, tc.want)
		}
	}
}

func TestCheckSupersede(t *testing.T) {
	t.Parallel()

	rev := func(state catalog.RevisionState, j job.ID) *catalog.Revision {
		var r catalog.Revision

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
		old  *catalog.Revision
		job  *job.Job
		want error
	}{
		{"accepted with no job", rev(catalog.StateAccepted, ""), nil, nil},
		{"queued", rev(catalog.StateQueued, "job-1"), nil, nil},
		{
			"a review request with no merge", rev(catalog.StateAwaitingMerge, "job-1"),
			withDelivery(delivered(job.DeliveryReviewRequest, 0)), nil,
		},
		{"a revision from git that waits for its state", rev(catalog.StateAwaitingState, ""), nil, nil},
		{"a job in git for good", rev(catalog.StateAwaitingState, "job-1"), nil, catalog.ErrConflict},
		{
			"a merge that the revision did not see yet", rev(catalog.StateAwaitingMerge, "job-1"),
			withDelivery(delivered(job.DeliveryReviewRequest, time.Hour)), catalog.ErrConflict,
		},
		{
			"a direct push during a run", rev(catalog.StateRunning, "job-1"),
			withDelivery(delivered(job.DeliveryDirectPush, 0)), catalog.ErrConflict,
		},
		{"an ended revision", rev(catalog.StateApplied, "job-1"), nil, catalog.ErrFinished},
	}

	for _, tc := range cases {
		err := catalog.CheckSupersede(tc.old, tc.job)
		if !errors.Is(err, tc.want) || (tc.want == nil && err != nil) {
			t.Fatalf("%s: CheckSupersede() = %v, want %v", tc.name, err, tc.want)
		}
	}
}

func TestRevisionDeadline(t *testing.T) {
	t.Parallel()

	at := time.Unix(1_790_000_000, 0).UTC()
	day := 24 * time.Hour

	if got := catalog.RevisionDeadline(catalog.OriginGit, at); !got.Equal(at.Add(15 * day)) {
		t.Fatalf("RevisionDeadline(git) = %v, want 14 days of state wait and 1 day of grace", got)
	}

	if got := catalog.RevisionDeadline(catalog.OriginAPI, at); !got.Equal(at.Add(31 * day)) {
		t.Fatalf("RevisionDeadline(api) = %v, want 30 days of job limits and 1 day of grace", got)
	}

	var r catalog.Revision

	r.State, r.Deadline = catalog.StateAwaitingState, catalog.RevisionDeadline(catalog.OriginGit, at)

	if r.Expired(r.Deadline.Add(-time.Second)) || !r.Expired(r.Deadline) {
		t.Fatal("Expired() does not end the revision at its deadline")
	}

	r.State = catalog.StateApplied
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
		end      catalog.RevisionState
		deadline time.Time
		err      error
	}{
		{
			name: "before the deadline", job: "job-1", j: jobIn(job.StateRunning), now: deadline.Add(-time.Minute),
			end: "", deadline: time.Time{}, err: nil,
		},
		{
			name: "no job expires", job: "", j: nil, now: after,
			end: catalog.StateExpired, deadline: time.Time{}, err: nil,
		},
		{
			name: "a running job moves the deadline", job: "job-1", j: jobIn(job.StateRunning), now: after,
			end: "", deadline: after.Add(quota.ReservationGrace), err: nil,
		},
		{
			name: "a job at a gate moves the deadline", job: "job-1", j: jobIn(job.StateAwaitingApproval), now: after,
			end: "", deadline: after.Add(quota.ReservationGrace), err: nil,
		},
		{
			name: "a job that succeeded applies", job: "job-1", j: jobIn(job.StateSucceeded), now: after,
			end: catalog.StateApplied, deadline: time.Time{}, err: nil,
		},
		{
			name: "a job that was canceled cancels", job: "job-1", j: jobIn(job.StateCanceled), now: after,
			end: catalog.StateCanceled, deadline: time.Time{}, err: nil,
		},
		{
			name: "a job that the caller did not read", job: "job-1", j: nil, now: after,
			end: "", deadline: time.Time{}, err: catalog.ErrInvalid,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var r catalog.Revision

			r.State, r.Job, r.Deadline = catalog.StateRunning, tt.job, deadline

			got, err := catalog.ExpiryOf(&r, tt.j, tt.now)
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
		want  catalog.RevisionState
		final bool
	}{
		{err: job.ErrPolicyDenied, want: catalog.StateRejected, final: true},
		{err: fmt.Errorf("start: %w", job.ErrFrozen), want: catalog.StateRejected, final: true},
		{err: job.ErrAgentDenied, want: catalog.StateRejected, final: true},
		{err: job.ErrLimit, want: catalog.StateFailed, final: true},
		{err: job.ErrInvalid, want: catalog.StateFailed, final: true},
		{err: job.ErrUnsupported, want: catalog.StateFailed, final: true},
		{err: errDatabase, want: "", final: false},
	}

	for _, tc := range cases {
		got, final := catalog.StartFailure(tc.err)
		if got != tc.want || final != tc.final {
			t.Fatalf("StartFailure(%v) = %q, %v, want %q, %v", tc.err, got, final, tc.want, tc.final)
		}
	}
}

func TestGitPath(t *testing.T) {
	t.Parallel()

	ref := catalog.RequestRef{Namespace: "payments", Blueprint: "postgres", Name: "orders-db"}
	if got := catalog.GitPath(ref); got != "catalog/payments/postgres/orders-db/request.json" {
		t.Fatalf("GitPath() = %q", got)
	}

	back, err := catalog.ParseGitPath(catalog.GitPath(ref))
	if err != nil || back != ref {
		t.Fatalf("ParseGitPath(GitPath()) = %+v, %v", back, err)
	}

	for _, bad := range []string{
		"catalog/payments/postgres/orders-db/request.yaml",
		"infra/payments/postgres/orders-db/request.json",
		"catalog/payments/postgres/request.json",
		"catalog/payments/postgres/Orders/request.json",
		"catalog/pay ments/postgres/orders-db/request.json",
		"catalog/payments/postgres/orders-db/x/request.json",
	} {
		if _, err := catalog.ParseGitPath(bad); !errors.Is(err, catalog.ErrInvalid) {
			t.Fatalf("ParseGitPath(%q) = %v, want ErrInvalid", bad, err)
		}
	}

	d := parseDocument(t, string(readFile(t, "orders-db.request.json")))
	if err := catalog.CheckPath(catalog.GitPath(ref), &d); err != nil {
		t.Fatalf("CheckPath() = %v", err)
	}

	other := catalog.RequestRef{Namespace: "payments", Blueprint: "postgres", Name: "users-db"}
	if err := catalog.CheckPath(catalog.GitPath(other), &d); !errors.Is(err, catalog.ErrPathMismatch) {
		t.Fatalf("CheckPath(another name) = %v, want ErrPathMismatch", err)
	}
}

func TestRequirements(t *testing.T) {
	t.Parallel()

	u := unit(t, "databases/orders-db", "ws-1")
	bp := catalog.Object{
		Unit:      identity.Object{},
		Kind:      catalog.ObjectBlueprint,
		Namespace: "payments",
		Blueprint: "postgres",
	}
	ns := catalog.Object{Unit: identity.Object{}, Kind: catalog.ObjectNamespace, Namespace: "payments", Blueprint: ""}
	on := catalog.Object{Unit: u, Kind: catalog.ObjectUnit, Namespace: "payments", Blueprint: "postgres"}

	target := func(op catalog.Operation, unit identity.Object, breakGlass bool) catalog.Target {
		return catalog.Target{
			Unit:       unit,
			Namespace:  "payments",
			Blueprint:  "postgres",
			Operation:  op,
			BreakGlass: breakGlass,
		}
	}

	cases := []struct {
		name string
		op   catalog.Op
		t    catalog.Target
		want []catalog.Requirement
		err  error
	}{
		{
			"create", catalog.OpSubmit, target(catalog.OperationCreate, identity.Object{}, false),
			[]catalog.Requirement{{Action: catalog.ActionCatalogRequest, Object: bp}},
			nil,
		},
		{
			"update", catalog.OpSubmit, target(catalog.OperationUpdate, u, false),
			[]catalog.Requirement{{Action: catalog.ActionUnitEditInputs, Object: on}},
			nil,
		},
		{
			"destroy with break-glass", catalog.OpSubmit, target(catalog.OperationDestroy, u, true),
			[]catalog.Requirement{
				{Action: catalog.ActionUnitRequestDestroy, Object: on},
				{Action: job.ActionNamespaceBreakFreeze, Object: ns},
			},
			nil,
		},
		{
			"an update with no unit", catalog.OpSubmit, target(catalog.OperationUpdate, identity.Object{}, false), nil,
			catalog.ErrInvalid,
		},
		{
			"options of a create", catalog.OpOptions, target(catalog.OperationCreate, identity.Object{}, false),
			[]catalog.Requirement{{Action: catalog.ActionCatalogRequest, Object: bp}},
			nil,
		},
		{
			"options of an update", catalog.OpOptions, target(catalog.OperationUpdate, u, false),
			[]catalog.Requirement{{Action: catalog.ActionUnitEditInputs, Object: on}},
			nil,
		},
		{
			"check of an update", catalog.OpCheck, target(catalog.OperationUpdate, u, true),
			[]catalog.Requirement{{Action: catalog.ActionUnitEditInputs, Object: on}},
			nil,
		},
		{
			"check of a destroy", catalog.OpCheck, target(catalog.OperationDestroy, u, false),
			[]catalog.Requirement{{Action: catalog.ActionUnitRequestDestroy, Object: on}},
			nil,
		},
		{"options with no operation", catalog.OpOptions, target("", u, false), nil, catalog.ErrInvalid},
		{
			"cancel", catalog.OpCancel, target("", u, false),
			[]catalog.Requirement{{Action: job.ActionJobCancel, Object: on}},
			nil,
		},
		{
			"render of a create", catalog.OpRender, target(catalog.OperationCreate, identity.Object{}, false),
			[]catalog.Requirement{
				{Action: catalog.ActionCatalogRender, Object: bp},
				{Action: catalog.ActionCatalogRequest, Object: bp},
			},
			nil,
		},
		{
			"render of an update", catalog.OpRender, target(catalog.OperationUpdate, u, false),
			[]catalog.Requirement{
				{Action: catalog.ActionCatalogRender, Object: on},
				{Action: catalog.ActionUnitEditInputs, Object: on},
			},
			nil,
		},
		{
			"render of a destroy", catalog.OpRender, target(catalog.OperationDestroy, u, false),
			[]catalog.Requirement{
				{Action: catalog.ActionCatalogRender, Object: on},
				{Action: catalog.ActionUnitRequestDestroy, Object: on},
			},
			nil,
		},
		{
			"render that stores no revision", catalog.OpRender, target(catalog.OperationNone, u, false),
			[]catalog.Requirement{{Action: catalog.ActionCatalogRender, Object: on}},
			nil,
		},
		{
			"a render of an update with no unit", catalog.OpRender,
			target(catalog.OperationUpdate, identity.Object{}, false), nil, catalog.ErrInvalid,
		},
		{
			"list requests", catalog.OpListRequests, target("", identity.Object{}, false),
			[]catalog.Requirement{{Action: catalog.ActionCatalogRead, Object: ns}},
			nil,
		},
		{
			"manage policy", catalog.OpManagePolicy, target("", identity.Object{}, false),
			[]catalog.Requirement{{Action: catalog.ActionNamespaceManagePolicy, Object: ns}},
			nil,
		},
		{"an unknown operation", "approve", target("", u, false), nil, catalog.ErrInvalid},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := catalog.Requirements(tc.op, tc.t)
			if !errors.Is(err, tc.err) || (tc.err == nil && err != nil) || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Requirements() = %+v, %v, want %+v, %v", got, err, tc.want, tc.err)
			}
		})
	}
}

func TestPublishRequirements(t *testing.T) {
	t.Parallel()

	b := parseBlueprint(t, readFile(t, "postgres.blueprint.json"))
	got := catalog.PublishRequirements("payments", &b)

	// The publisher needs the publish action, and can attach the source
	// principal only when it can already act as it. It needs no grant on
	// the sources.
	if len(got) != 2 || got[0].Action != catalog.ActionBlueprintPublish ||
		got[1].Action != identity.ActionPrincipalManageTokens || got[1].Object.Kind != catalog.ObjectPrincipal {
		t.Fatalf("PublishRequirements() = %+v", got)
	}

	if o, ok := got[1].Object.Identity(); !ok || o.PrincipalID() != "catalog-reader" {
		t.Fatalf("Identity() of the source principal = %v, %v", o, ok)
	}

	if _, ok := got[0].Object.Identity(); ok {
		t.Fatal("a blueprint has an identity object")
	}

	b.Inputs = b.Inputs[:1]
	b.SourcePrincipal = ""

	if got := catalog.PublishRequirements("payments", &b); len(got) != 1 {
		t.Fatalf("PublishRequirements(no source) = %+v, want the publish action only", got)
	}
}

func TestSourceRequirements(t *testing.T) {
	t.Parallel()

	b := parseBlueprint(t, readFile(t, "postgres.blueprint.json"))

	// Two sources of blueprints need unit:read on the namespace once.
	got := catalog.SourceRequirements("payments", &b)
	if len(got) != 1 || got[0].Action != identity.ActionUnitRead || got[0].Object.Kind != catalog.ObjectNamespace {
		t.Fatalf("SourceRequirements() = %+v", got)
	}

	b.Inputs[1].Choice.Source.Blueprint = ""
	b.Inputs[1].Choice.Source.Collection = "networks"

	got = catalog.SourceRequirements("payments", &b)
	if len(got) != 2 || got[0].Object.Kind != catalog.ObjectCollection ||
		got[1].Object.Kind != catalog.ObjectNamespace {
		t.Fatalf("SourceRequirements(a collection) = %+v", got)
	}
}

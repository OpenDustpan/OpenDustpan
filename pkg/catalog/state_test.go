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
		{from: catalog.StateRunning, to: catalog.StateSuperseded, want: catalog.ErrTransition},
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
			"options", catalog.OpOptions, target("", identity.Object{}, false),
			[]catalog.Requirement{{Action: catalog.ActionCatalogRequest, Object: bp}},
			nil,
		},
		{
			"cancel", catalog.OpCancel, target("", u, false),
			[]catalog.Requirement{{Action: job.ActionJobCancel, Object: on}},
			nil,
		},
		{
			"render", catalog.OpRender, target("", u, false),
			[]catalog.Requirement{{Action: catalog.ActionCatalogRender, Object: on}},
			nil,
		},
		{"a render with no unit", catalog.OpRender, target("", identity.Object{}, false), nil, catalog.ErrInvalid},
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

	// Two sources of blueprints need unit:read on the namespace once.
	if len(got) != 2 || got[0].Action != catalog.ActionBlueprintPublish || got[1].Action != identity.ActionUnitRead ||
		got[1].Object.Kind != catalog.ObjectNamespace {
		t.Fatalf("PublishRequirements() = %+v", got)
	}

	if _, ok := got[0].Object.Identity(); ok {
		t.Fatal("a blueprint has an identity object")
	}
}

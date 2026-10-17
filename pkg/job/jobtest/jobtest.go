// Package jobtest is the conformance suite for job.Store and
// job.FreezeStore implementations. Every store adapter runs it:
//
//	func TestJobConformance(t *testing.T) {
//		t.Parallel()
//		jobtest.Run(t, func(t *testing.T) jobtest.Harness { return newHarness(t) })
//	}
//
// The suite comes before the first adapter. Its cases state the rules of
// the ports. The cases of job.Claimer, job.Recorder and job.ArtifactStore
// come with their first implementation, because they need the blob store
// and the data keys.
package jobtest

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Harness is one fresh, empty database for one case. The stores share the
// database, as the store adapters of one installation do.
type Harness struct {
	Jobs    job.Store
	Durable durable.Store
	Freezes job.FreezeStore
	Domain  Domain
}

// Domain makes the rows that a job needs and that other ports own.
type Domain interface {
	// CreateNamespace creates the namespace name and returns its fence.
	CreateNamespace(ctx context.Context, name namespace.Name) (namespace.Fence, error)
	// CreateUnit creates a unit of tool in the namespace of fence and
	// returns its reference, with its stable unit ID set.
	CreateUnit(ctx context.Context, fence namespace.Fence, tool, name string) (identity.Object, error)
	// BumpFence increments the failover version of the namespace of fence,
	// as a failover does, and returns the new fence.
	BumpFence(ctx context.Context, fence namespace.Fence) (namespace.Fence, error)
}

// Factory returns a fresh harness for one case.
type Factory func(t *testing.T) Harness

// Run executes the conformance suite against harnesses from newHarness.
func Run(t *testing.T, newHarness Factory) {
	t.Helper()

	cases := []struct {
		name string
		run  func(t *testing.T, h Harness)
	}{
		{"create stores a queued job and get returns it", testCreateGet},
		{"get of an unknown ID is ErrNotFound", testGetUnknown},
		{"two creates with one key and one spec give one job", testCreateKeyOnce},
		{"a key with another spec conflicts", testCreateKeyConflict},
		{"create with a dispatch starts the durable operation", testCreateDispatch},
		{"a write on an older version is ErrConflict", testApplyConflict},
		{"an event that the state does not allow changes nothing", testApplyTransition},
		{"a second change dispatch on one unit is ErrUnitBusy", testUnitBusy},
		{"a plan dispatch does not hold the unit", testPlanDoesNotHold},
		{"a change dispatch in a freeze is ErrFrozen without break-glass", testFrozen},
		{"a cancel of a dispatched job cancels its operation", testCancelDispatched},
		{"a cancel of a running job requests the cancel of its operation", testCancelRunning},
		{"a stale fence changes nothing", testStaleFence},
		{"a state version closes only the delivered jobs of its unit", testObserveStateVersion},
		{"list filters by unit and state, newest first", testList},
		{"freezes list, end once, and validate", testFreezes},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.run(t, newHarness(t))
		})
	}
}

// fixture is a namespace with one OpenTofu unit.
type fixture struct {
	fence namespace.Fence
	unit  identity.Object
}

// setUp creates the namespace acme with the unit network.
func setUp(t *testing.T, h Harness) fixture {
	t.Helper()

	fence, err := h.Domain.CreateNamespace(t.Context(), "acme")
	if err != nil {
		t.Fatalf("CreateNamespace: %v", err)
	}

	unit, err := h.Domain.CreateUnit(t.Context(), fence, "opentofu", "network")
	if err != nil {
		t.Fatalf("CreateUnit: %v", err)
	}

	return fixture{fence: fence, unit: unit}
}

// newJob returns a queued runner job of op on unit with the key key.
func newJob(unit identity.Object, op job.Operation, key string) *job.Job {
	phases, err := job.DefaultPhases(job.ExecutorRunner, op)
	if err != nil {
		panic(err)
	}

	j := job.Job{
		ID:             job.ID(ident.NewID(job.IDPrefix)),
		Spec:           spec(unit, op),
		State:          job.StateQueued,
		Wait:           job.WaitNone,
		Reason:         "",
		Phases:         make([]job.Phase, len(phases)),
		Current:        0,
		StartedBy:      "al",
		BreakGlass:     nil,
		Delivery:       nil,
		Closed:         nil,
		IdempotencyKey: key,
		Version:        0,
		CreatedAt:      time.Time{},
		UpdatedAt:      time.Time{},
	}

	for i, k := range phases {
		j.Phases[i].Kind = k
	}

	return &j
}

// spec returns the spec of a runner job of op on unit.
func spec(unit identity.Object, op job.Operation) job.Spec {
	return job.Spec{
		Unit:      unit,
		Operation: op,
		Executor:  job.ExecutorRunner,
		Location: job.Location{
			Connection: "github", Repository: "acme/infra", Ref: "main", Directory: "network", Revision: "abc123",
		},
		Steps: []job.Step{{
			Name: "plan", Role: job.RoleTool, Phase: job.PhasePlan, Args: []string{"tofu", "plan"}, Env: nil,
			Dir: "", Timeout: 0, AllowFailure: false, ChangesResources: false, Outputs: nil,
		}},
		Pool:     "rpl-default",
		Edits:    nil,
		Delivery: "",
	}
}

// entry returns a valid audit entry for action on unit.
func entry(unit identity.Object, action identity.Action) audit.Entry {
	return audit.Entry{
		At: time.Now().UTC(), Namespace: unit.Namespace(), Subject: "al", IDPSubject: "", TokenID: "",
		Action: string(action), Object: unit.String(), Version: "", ValuePath: "", ValueCount: nil, Changes: nil,
		SourceIP: "", UserAgent: "", Channel: audit.ChannelAPI, Kind: audit.KindChange,
		Outcome: audit.OutcomeSuccess, RequestID: "", ObjectID: "", PrevHash: "", Hash: "", Sequence: 0,
		FenceVersion: 0,
	}
}

// firstDispatch returns the durable start request of the first dispatch of
// the first phase of the runner job j.
func firstDispatch(j *job.Job) *durable.StartRequest {
	return &durable.StartRequest{
		Key: job.DispatchKey(j.ID, 0, 1), Kind: job.RunnerKind(j.Spec.Pool), Version: 1, Input: nil,
	}
}

// create stores j and fails t on an error.
func create(t *testing.T, h Harness, f fixture, j *job.Job, d *durable.StartRequest) job.Job {
	t.Helper()

	stored, created, err := h.Jobs.Create(t.Context(), *j, d, f.fence,
		[]audit.Entry{entry(f.unit, job.ActionUnitRunApply)})
	if err != nil || !created {
		t.Fatalf("Create = created %v, error %v; want a new job", created, err)
	}

	return stored
}

// apply makes the change and fails t on an error.
func apply(t *testing.T, h Harness, f fixture, c job.Change) job.Job {
	t.Helper()

	got, err := h.Jobs.Apply(t.Context(), c, f.fence, []audit.Entry{entry(f.unit, job.ActionJobRead)})
	if err != nil {
		t.Fatalf("Apply(%s) = %v", c.Event, err)
	}

	return got
}

// change returns the change of j by ev.
func change(j *job.Job, ev job.Event) job.Change {
	return job.Change{
		Job: j.ID, Version: j.Version, Event: ev, Wait: job.WaitNone, Reason: "", Dispatch: nil, Executor: "",
		Delivery: nil, Approval: nil,
	}
}

func testCreateGet(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	stored := create(t, h, f, newJob(f.unit, job.OperationPlan, ""), nil)

	if stored.State != job.StateQueued || stored.Version < 1 || stored.CreatedAt.IsZero() {
		t.Fatalf("Create = state %s, version %d, created %v; want queued, a version and a time",
			stored.State, stored.Version, stored.CreatedAt)
	}

	got, err := h.Jobs.Get(t.Context(), stored.ID)
	if err != nil {
		t.Fatalf("Get = %v", err)
	}

	if got.ID != stored.ID || got.Spec.Unit.String() != f.unit.String() || len(got.Phases) != 1 ||
		got.Spec.Steps[0].Args[0] != "tofu" {
		t.Fatalf("Get = %+v, want the stored job", got)
	}
}

func testGetUnknown(t *testing.T, h Harness) {
	t.Helper()

	if _, err := h.Jobs.Get(t.Context(), "job-unknown"); !errors.Is(err, job.ErrNotFound) {
		t.Fatalf("Get = %v, want ErrNotFound", err)
	}
}

func testCreateKeyOnce(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	first := create(t, h, f, newJob(f.unit, job.OperationPlan, "start-1"), nil)

	again, created, err := h.Jobs.Create(t.Context(), *newJob(f.unit, job.OperationPlan, "start-1"), nil, f.fence,
		[]audit.Entry{entry(f.unit, job.ActionUnitRunPlan)})
	if err != nil || created || again.ID != first.ID {
		t.Fatalf("second Create = %s, created %v, error %v; want %s", again.ID, created, err, first.ID)
	}
}

func testCreateKeyConflict(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	create(t, h, f, newJob(f.unit, job.OperationPlan, "start-1"), nil)

	_, _, err := h.Jobs.Create(t.Context(), *newJob(f.unit, job.OperationApply, "start-1"), nil, f.fence,
		[]audit.Entry{entry(f.unit, job.ActionUnitRunApply)})
	if !errors.Is(err, job.ErrKeyConflict) {
		t.Fatalf("Create with another spec = %v, want ErrKeyConflict", err)
	}
}

func testCreateDispatch(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	j := newJob(f.unit, job.OperationPlan, "")
	stored := create(t, h, f, j, firstDispatch(j))

	if stored.State != job.StateDispatched || stored.Phases[0].Operation == "" {
		t.Fatalf("Create = state %s, operation %q; want dispatched with an operation",
			stored.State, stored.Phases[0].Operation)
	}

	op, err := h.Durable.Get(t.Context(), durable.ID(stored.Phases[0].Operation))
	if err != nil || op.Key != job.DispatchKey(j.ID, 0, 1) || op.Kind != job.RunnerKind(j.Spec.Pool) {
		t.Fatalf("durable Get = %+v, %v; want the dispatch of phase 0", op, err)
	}
}

func testApplyConflict(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	stored := create(t, h, f, newJob(f.unit, job.OperationPlan, ""), nil)
	stale := change(&stored, job.EventCancel)
	stale.Version = stored.Version - 1

	if _, err := h.Jobs.Apply(t.Context(), stale, f.fence, nil); !errors.Is(err, job.ErrConflict) {
		t.Fatalf("Apply on an older version = %v, want ErrConflict", err)
	}

	assertState(t, h, stored.ID, job.StateQueued)
}

func testApplyTransition(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	stored := create(t, h, f, newJob(f.unit, job.OperationPlan, ""), nil)

	_, err := h.Jobs.Apply(t.Context(), change(&stored, job.EventStateObserved), f.fence, nil)
	if !errors.Is(err, job.ErrTransition) {
		t.Fatalf("Apply(state-observed) on queued = %v, want ErrTransition", err)
	}

	assertState(t, h, stored.ID, job.StateQueued)
}

func testUnitBusy(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	first := runApplyPhase(t, h, f)
	second := create(t, h, f, newJob(f.unit, job.OperationRefresh, ""), nil)

	c := change(&second, job.EventDispatch)
	c.Dispatch = firstDispatch(&second)

	if _, err := h.Jobs.Apply(t.Context(), c, f.fence, nil); !errors.Is(err, job.ErrUnitBusy) {
		t.Fatalf("second change dispatch while %s holds the unit = %v, want ErrUnitBusy", first.ID, err)
	}

	assertState(t, h, second.ID, job.StateQueued)
}

// runApplyPhase creates a refresh job, whose only phase changes state, and
// dispatches it, so that it holds its unit.
func runApplyPhase(t *testing.T, h Harness, f fixture) job.Job {
	t.Helper()

	j := create(t, h, f, newJob(f.unit, job.OperationRefresh, ""), nil)
	c := change(&j, job.EventDispatch)
	c.Dispatch = firstDispatch(&j)

	return apply(t, h, f, c)
}

func testPlanDoesNotHold(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	runApplyPhase(t, h, f)

	plan := create(t, h, f, newJob(f.unit, job.OperationPlan, ""), nil)
	c := change(&plan, job.EventDispatch)
	c.Dispatch = firstDispatch(&plan)

	if got := apply(t, h, f, c); got.State != job.StateDispatched {
		t.Fatalf("plan dispatch = %s, want dispatched", got.State)
	}
}

func testFrozen(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	freeze := job.Freeze{
		ID: ident.NewID("frz-"), Namespace: f.fence.Namespace, Reason: "release week",
		StartsAt: time.Now().Add(-time.Hour), EndsAt: time.Time{}, CreatedBy: "al", EndedBy: "",
	}

	err := h.Freezes.CreateFreeze(t.Context(), freeze, f.fence,
		entry(f.unit, job.ActionNamespaceManageFreezes))
	if err != nil {
		t.Fatalf("CreateFreeze = %v", err)
	}

	plain := create(t, h, f, newJob(f.unit, job.OperationRefresh, ""), nil)
	c := change(&plain, job.EventDispatch)
	c.Dispatch = firstDispatch(&plain)

	if _, err := h.Jobs.Apply(t.Context(), c, f.fence, nil); !errors.Is(err, job.ErrFrozen) {
		t.Fatalf("change dispatch in a freeze = %v, want ErrFrozen", err)
	}

	withGlass := newJob(f.unit, job.OperationRefresh, "")
	withGlass.BreakGlass = &job.BreakGlass{Reason: "outage", Freeze: freeze.ID}
	glass := create(t, h, f, withGlass, nil)
	c = change(&glass, job.EventDispatch)
	c.Dispatch = firstDispatch(&glass)

	if got := apply(t, h, f, c); got.State != job.StateDispatched {
		t.Fatalf("break-glass dispatch = %s, want dispatched", got.State)
	}
}

func testCancelDispatched(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	j := runApplyPhase(t, h, f)

	got := apply(t, h, f, change(&j, job.EventCancel))
	if got.State != job.StateCanceled {
		t.Fatalf("cancel of a dispatched job = %s, want canceled", got.State)
	}

	op, err := h.Durable.Get(t.Context(), durable.ID(got.Phases[0].Operation))
	if err != nil || op.State != durable.StateCanceled {
		t.Fatalf("operation = %s, %v; want canceled", op.State, err)
	}
}

func testCancelRunning(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	j := runApplyPhase(t, h, f)

	_, err := h.Durable.Claim(t.Context(), durable.ClaimRequest{
		Owner: "runner-1", Kinds: []durable.Kind{job.RunnerKind(j.Spec.Pool)}, MaxVersions: nil, Lease: time.Minute,
	})
	if err != nil {
		t.Fatalf("durable Claim = %v", err)
	}

	j = apply(t, h, f, change(&j, job.EventClaim))
	got := apply(t, h, f, change(&j, job.EventCancel))

	if got.State != job.StateCanceling {
		t.Fatalf("cancel of a running job = %s, want canceling", got.State)
	}

	op, err := h.Durable.Get(t.Context(), durable.ID(got.Phases[0].Operation))
	if err != nil || op.State != durable.StateRunning || !op.CancelRequested {
		t.Fatalf("operation = %s, cancel %v, %v; want running with a cancel request", op.State,
			op.CancelRequested, err)
	}
}

func testStaleFence(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	stored := create(t, h, f, newJob(f.unit, job.OperationPlan, ""), nil)

	if _, err := h.Domain.BumpFence(t.Context(), f.fence); err != nil {
		t.Fatalf("BumpFence = %v", err)
	}

	if _, err := h.Jobs.Apply(t.Context(), change(&stored, job.EventCancel), f.fence, nil); !errors.Is(err,
		namespace.ErrFenced) {
		t.Fatalf("Apply with a stale fence = %v, want ErrFenced", err)
	}

	assertState(t, h, stored.ID, job.StateQueued)

	_, _, err := h.Jobs.Create(t.Context(), *newJob(f.unit, job.OperationPlan, ""), nil, f.fence, nil)
	if !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("Create with a stale fence = %v, want ErrFenced", err)
	}
}

func testObserveStateVersion(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)

	other, err := h.Domain.CreateUnit(t.Context(), f.fence, "opentofu", "dns")
	if err != nil {
		t.Fatalf("CreateUnit = %v", err)
	}

	delivered := deliver(t, h, f, f.unit)
	elsewhere := deliver(t, h, f, other)
	before := delivered.Delivery.DeliveredAt.Add(-time.Second)

	closed, err := h.Jobs.ObserveStateVersion(t.Context(), job.StateVersion{
		Unit: f.unit, VersionID: "sv-early", Commit: "", At: before, Verified: false,
	}, f.fence)
	if err != nil || len(closed) != 0 {
		t.Fatalf("a state version from before the delivery closed %d jobs, %v; want none", len(closed), err)
	}

	closed, err = h.Jobs.ObserveStateVersion(t.Context(), job.StateVersion{
		Unit: f.unit, VersionID: "sv-1", Commit: "", At: time.Now().Add(time.Second), Verified: false,
	}, f.fence)
	if err != nil || len(closed) != 1 || closed[0].ID != delivered.ID {
		t.Fatalf("ObserveStateVersion closed %v, %v; want %s", closed, err, delivered.ID)
	}

	if closed[0].State != job.StateSucceeded || closed[0].Closed == nil || closed[0].Closed.VersionID != "sv-1" {
		t.Fatalf("closed job = %s, %+v; want succeeded and closed by sv-1", closed[0].State, closed[0].Closed)
	}

	assertState(t, h, elsewhere.ID, job.StateAwaitingState)
}

// deliver stores a write-back job of unit that delivered and waits for a
// state version.
func deliver(t *testing.T, h Harness, f fixture, unit identity.Object) job.Job {
	t.Helper()

	j := newJob(unit, job.OperationApply, "")
	j.Spec.Executor, j.Spec.Steps, j.Spec.Pool, j.Spec.Delivery = job.ExecutorWriteBack, nil, "",
		job.DeliveryDirectPush
	j.Spec.Edits = []job.FileEdit{{Path: "network.tfvars.json", Keys: map[string][]byte{"size": []byte(`"large"`)}}}
	j.Phases = j.Phases[:1]
	j.Phases[0].Kind = job.PhaseDeliver

	stored := create(t, h, f, j, &durable.StartRequest{
		Key: job.DispatchKey(j.ID, 0, 1), Kind: job.KindWriteBack, Version: 1, Input: nil,
	})

	_, err := h.Durable.Claim(t.Context(), durable.ClaimRequest{
		Owner: "instance-a", Kinds: []durable.Kind{job.KindWriteBack}, MaxVersions: nil, Lease: time.Minute,
	})
	if err != nil {
		t.Fatalf("durable Claim = %v", err)
	}

	claim := change(&stored, job.EventClaim)
	claim.Executor = "instance-a"
	stored = apply(t, h, f, claim)

	done := change(&stored, job.EventDelivered)
	done.Delivery = &job.DeliveryResult{
		Delivery: job.DeliveryDirectPush, Commit: "def456", Branch: "main", ReviewRequest: "",
		MergedAt: time.Time{}, DeliveredAt: time.Now().UTC(),
	}
	stored = apply(t, h, f, done)

	if stored.State != job.StateAwaitingState || stored.Delivery == nil || stored.Delivery.DeliveredAt.IsZero() {
		t.Fatalf("delivered job = %s, %+v; want awaiting-state with a delivery time", stored.State,
			stored.Delivery)
	}

	return stored
}

func testList(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	older := create(t, h, f, newJob(f.unit, job.OperationPlan, ""), nil)
	newer := create(t, h, f, newJob(f.unit, job.OperationPlan, ""), nil)
	apply(t, h, f, change(&newer, job.EventCancel))

	all, err := h.Jobs.List(t.Context(), job.Filter{
		Namespace: f.fence.Namespace, UnitID: "", States: nil, Limit: 0, After: "",
	})
	if err != nil || len(all) != 2 || all[0].ID != newer.ID || all[1].ID != older.ID {
		t.Fatalf("List = %v, %v; want %s then %s", ids(all), err, newer.ID, older.ID)
	}

	queued, err := h.Jobs.List(t.Context(), job.Filter{
		Namespace: f.fence.Namespace, UnitID: "", States: []job.State{job.StateQueued}, Limit: 0, After: "",
	})
	if err != nil || !slices.Equal(ids(queued), []job.ID{older.ID}) {
		t.Fatalf("List(queued) = %v, %v; want %s", ids(queued), err, older.ID)
	}
}

// ids returns the IDs of jobs, in order.
func ids(jobs []job.Job) []job.ID {
	out := make([]job.ID, 0, len(jobs))
	for i := range jobs {
		out = append(out, jobs[i].ID)
	}

	return out
}

func testFreezes(t *testing.T, h Harness) {
	t.Helper()

	f := setUp(t, h)
	e := entry(f.unit, job.ActionNamespaceManageFreezes)
	freeze := job.Freeze{
		ID: ident.NewID("frz-"), Namespace: f.fence.Namespace, Reason: "release week",
		StartsAt: time.Now(), EndsAt: time.Time{}, CreatedBy: "al", EndedBy: "",
	}

	bad := freeze
	bad.Reason = ""

	if err := h.Freezes.CreateFreeze(t.Context(), bad, f.fence, e); !errors.Is(err, job.ErrInvalid) {
		t.Fatalf("CreateFreeze with no reason = %v, want ErrInvalid", err)
	}

	if err := h.Freezes.CreateFreeze(t.Context(), freeze, f.fence, e); err != nil {
		t.Fatalf("CreateFreeze = %v", err)
	}

	listed, err := h.Freezes.ListFreezes(t.Context(), f.fence.Namespace, time.Now().Add(-time.Hour))
	if err != nil || len(listed) != 1 || listed[0].ID != freeze.ID {
		t.Fatalf("ListFreezes = %v, %v; want %s", listed, err, freeze.ID)
	}

	if err := h.Freezes.EndFreeze(t.Context(), freeze.ID, "bo", f.fence, e); err != nil {
		t.Fatalf("EndFreeze = %v", err)
	}

	if err := h.Freezes.EndFreeze(t.Context(), freeze.ID, "bo", f.fence, e); !errors.Is(err, job.ErrFinished) {
		t.Fatalf("second EndFreeze = %v, want ErrFinished", err)
	}

	if err := h.Freezes.EndFreeze(t.Context(), "frz-unknown", "bo", f.fence, e); !errors.Is(err, job.ErrNotFound) {
		t.Fatalf("EndFreeze of an unknown ID = %v, want ErrNotFound", err)
	}
}

// assertState fails t when the job id is not in state want.
func assertState(t *testing.T, h Harness, id job.ID, want job.State) {
	t.Helper()

	got, err := h.Jobs.Get(t.Context(), id)
	if err != nil || got.State != want {
		t.Fatalf("job %s = %s, %v; want %s", id, got.State, err, want)
	}
}

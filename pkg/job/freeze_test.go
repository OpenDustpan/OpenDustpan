package job_test

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/internal/store/sqlite"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// TestFreezeGuardAtJobStart checks the freeze guard of the real SQLite store:
// a start and a phase dispatch of a phase that changes the unit are refused
// with job.ErrFrozen during an active freeze unless the job has a break-glass,
// a plan start and a cancel are not refused, and a second create of a freeze
// is refused with job.ErrFreezeActive. Each subtest has its own namespace, so
// the subtests run in parallel.
func TestFreezeGuardAtJobStart(t *testing.T) {
	t.Parallel()

	st, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := st.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	cases := []struct {
		name string
		run  func(t *testing.T, st *sqlite.Store)
	}{
		{"start of a changing phase is refused", refusesStart},
		{"plan start passes", passesPlanStart},
		{"start with break-glass passes", passesBreakGlass},
		{"cancel succeeds and phase dispatch is refused", refusesCancelAndDispatch},
		{"second create is refused", refusesSecondCreate},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tc.run(t, st)
		})
	}
}

// refusesStart checks that a start of a phase that changes the unit in a frozen
// namespace is job.ErrFrozen. The store stores nothing for it.
func refusesStart(t *testing.T, st *sqlite.Store) {
	t.Helper()

	g := newFreezeGuard(t, st, "fzstart")
	j := newFreezeJob(g.unit)

	_, _, err := g.jobs.Create(t.Context(), *j, dispatchOfFreezeJob(j), g.fence, nil)
	if !errors.Is(err, job.ErrFrozen) {
		t.Fatalf("Create with a dispatch = %v, want ErrFrozen", err)
	}

	if _, err = g.jobs.Get(t.Context(), j.ID); !errors.Is(err, job.ErrNotFound) {
		t.Fatalf("Get after the refused start = %v, want ErrNotFound", err)
	}
}

// passesPlanStart checks that a plan start in a frozen namespace is accepted:
// a plan changes no resource, so the freeze does not refuse it.
func passesPlanStart(t *testing.T, st *sqlite.Store) {
	t.Helper()

	g := newFreezeGuard(t, st, "fzplan")
	j := newFreezeJobOf(g.unit, "", job.OperationPlan)

	stored, created, err := g.jobs.Create(t.Context(), *j, dispatchOfFreezeJob(j), g.fence, nil)
	if err != nil || !created || stored.State != job.StateDispatched {
		t.Fatalf("plan Create = %s, created %v, %v; want dispatched", stored.State, created, err)
	}
}

// passesBreakGlass checks that a start with a break-glass of the freeze is
// dispatched.
func passesBreakGlass(t *testing.T, st *sqlite.Store) {
	t.Helper()

	g := newFreezeGuard(t, st, "fzglass")
	j := newFreezeJob(g.unit)
	j.BreakGlass = &job.BreakGlass{Reason: "outage", Freeze: g.freeze.ID}

	stored, created, err := g.jobs.Create(t.Context(), *j, dispatchOfFreezeJob(j), g.fence, nil)
	if err != nil || !created || stored.State != job.StateDispatched {
		t.Fatalf("break-glass Create = %s, created %v, %v; want dispatched", stored.State, created, err)
	}
}

// refusesCancelAndDispatch checks that a phase dispatch of a queued job in a
// frozen namespace is job.ErrFrozen and changes nothing, and that a cancel of
// the same job still succeeds.
func refusesCancelAndDispatch(t *testing.T, st *sqlite.Store) {
	t.Helper()

	g := newFreezeGuard(t, st, "fzcancel")

	queued, _, err := g.jobs.Create(t.Context(), *newFreezeJob(g.unit), nil, g.fence, nil)
	if err != nil {
		t.Fatalf("Create queued = %v", err)
	}

	dispatch := freezeChange(&queued, job.EventDispatch)
	dispatch.Dispatch = dispatchOfFreezeJob(&queued)

	if _, err = g.jobs.Apply(t.Context(), dispatch, g.fence, nil); !errors.Is(err, job.ErrFrozen) {
		t.Fatalf("dispatch = %v, want ErrFrozen", err)
	}

	got, err := g.jobs.Get(t.Context(), queued.ID)
	if err != nil || got.State != job.StateQueued {
		t.Fatalf("job after the refused dispatch = %s, %v; want queued", got.State, err)
	}

	cancel := freezeChange(&queued, job.EventCancel)
	if _, err = g.jobs.Apply(t.Context(), cancel, g.fence, nil); err != nil {
		t.Fatalf("cancel in a freeze = %v, want nil", err)
	}

	got, err = g.jobs.Get(t.Context(), queued.ID)
	if err != nil || got.State != job.StateCanceled {
		t.Fatalf("job after the cancel = %s, %v; want canceled", got.State, err)
	}
}

// refusesSecondCreate checks that a second create of a freeze in a namespace
// with an active freeze is job.ErrFreezeActive that names the active freeze.
// The unique index refuses a second open freeze too, but it names the refused
// freeze, so naming the active freeze proves the refusal comes from the check.
func refusesSecondCreate(t *testing.T, st *sqlite.Store) {
	t.Helper()

	g := newFreezeGuard(t, st, "fzsecond")

	second := g.freeze
	second.ID = ident.NewID("frz-")

	err := g.freezes.CreateFreeze(t.Context(), second, g.fence, freezeEntry(g.unit))
	if !errors.Is(err, job.ErrFreezeActive) {
		t.Fatalf("second CreateFreeze = %v, want ErrFreezeActive", err)
	}

	if err == nil || !strings.Contains(err.Error(), g.freeze.ID) {
		t.Fatalf("second CreateFreeze = %v, want the active freeze %s", err, g.freeze.ID)
	}
}

// freezeGuard is one namespace with one OpenTofu unit and one active freeze.
type freezeGuard struct {
	jobs    job.Store
	freezes job.FreezeStore
	fence   namespace.Fence
	unit    identity.Object
	freeze  job.Freeze
}

// newFreezeGuard creates the namespace name with an OpenTofu unit network and
// an active open-ended freeze of it, in the store st.
func newFreezeGuard(t *testing.T, st *sqlite.Store, name string) freezeGuard {
	t.Helper()

	fence, unit := freezeFixture(t, st, name)
	stores := st.Jobs()

	fr := job.Freeze{
		ID: ident.NewID("frz-"), Namespace: fence.Namespace, Reason: "release week",
		StartsAt: time.Now().Add(-time.Hour), EndsAt: time.Time{}, CreatedBy: "al", EndedBy: "",
	}

	if err := stores.CreateFreeze(t.Context(), fr, fence, freezeEntry(unit)); err != nil {
		t.Fatalf("CreateFreeze = %v", err)
	}

	return freezeGuard{jobs: stores, freezes: stores, fence: fence, unit: unit, freeze: fr}
}

// freezeFixture creates the namespace name with an OpenTofu unit network in
// the store st, and returns its fence and the unit.
func freezeFixture(t *testing.T, st *sqlite.Store, name string) (namespace.Fence, identity.Object) {
	t.Helper()

	ctx := t.Context()
	now := time.UnixMilli(1_700_000_000_000).UTC()

	ns := namespace.Namespace{
		ID:                  namespace.NewID(),
		CreatedAt:           now,
		Name:                namespace.Name(name),
		PrimaryInstallation: "eu-west",
		FailoverVersion:     1,
		Overrides:           namespace.Overrides{Forced: false, Unlocked: false},
	}

	if err := st.CreateNamespace(ctx, ns); err != nil {
		t.Fatalf("CreateNamespace = %v", err)
	}

	fence := ns.Fence()

	ref, err := workspace.ParseName("network")
	if err != nil {
		t.Fatalf("ParseName = %v", err)
	}

	w := workspace.Workspace{
		NamespaceID:  "",
		CreatedAt:    now,
		UpdatedAt:    now,
		Lock:         workspace.Lock{LockedAt: time.Time{}, ID: "", Holder: "", Reason: ""},
		LastRelease:  workspace.NoRelease(),
		ID:           workspace.ID(ident.NewID("ws-")),
		Ref:          workspace.Ref{Namespace: fence.Namespace, Name: ref},
		Revision:     1,
		Overrides:    namespace.Overrides{Forced: false, Unlocked: false},
		Origin:       identity.StateOrigin{Job: "", Commit: ""},
		InCollection: false,
	}

	if err := st.CreateWorkspace(ctx, w, fence); err != nil {
		t.Fatalf("CreateWorkspace = %v", err)
	}

	return fence, freezeUnitID(t, st, fence.Namespace)
}

// freezeUnitID returns the reference of the OpenTofu unit network of ns, with
// its stable unit ID set as the store has it.
func freezeUnitID(t *testing.T, st *sqlite.Store, ns namespace.Name) identity.Object {
	t.Helper()

	units, err := st.ListUnits(t.Context(), ns)
	if err != nil {
		t.Fatalf("ListUnits = %v", err)
	}

	var unit identity.Object

	for i := range units {
		if units[i].Tool != "opentofu" || units[i].Name != "network" {
			continue
		}

		o, err := identity.UnitObject("opentofu", ns, "network")
		if err != nil {
			t.Fatalf("UnitObject = %v", err)
		}

		unit = o.WithUnitID(units[i].UnitID)

		return unit
	}

	t.Fatal("ListUnits has no unit network")

	return unit
}

// newFreezeJob returns a queued runner job of a refresh on unit. A refresh has
// one apply phase, which changes the unit, so a start of it is a changing start.
func newFreezeJob(unit identity.Object) *job.Job {
	return newFreezeJobOf(unit, "", job.OperationRefresh)
}

// newFreezeJobOf returns a queued runner job of operation op on unit.
func newFreezeJobOf(unit identity.Object, key string, op job.Operation) *job.Job {
	phases, err := job.DefaultPhases(job.ExecutorRunner, op)
	if err != nil {
		panic(err)
	}

	j := job.Job{
		ID:             job.ID(ident.NewID(job.IDPrefix)),
		NamespaceID:    "",
		Spec:           freezeSpec(unit, op),
		State:          job.StateQueued,
		Wait:           job.WaitNone,
		Reason:         "",
		Phases:         make([]job.Phase, len(phases)),
		Current:        0,
		StartedBy:      "al",
		Source:         job.SourceAPI,
		Agent:          "",
		AgentMode:      "",
		BreakGlass:     nil,
		Trigger:        nil,
		Delivery:       nil,
		Closed:         nil,
		Verification:   job.VerificationNone,
		IdempotencyKey: key,
		RequestHash:    "",
		Version:        0,
		CreatedAt:      time.Time{},
		UpdatedAt:      time.Time{},
		StateChangedAt: time.Time{},
	}

	for i, k := range phases {
		j.Phases[i].Kind = k
	}

	return &j
}

// freezeSpec returns the spec of a runner operation op of unit. Its one step
// is a plan step, which changes no resource.
func freezeSpec(unit identity.Object, op job.Operation) job.Spec {
	return job.Spec{
		Unit:      unit,
		Operation: op,
		Executor:  job.ExecutorRunner,
		Location: job.Location{
			Connection: "github", Repository: "acme/infra", Ref: "main", Directory: "network", Revision: "abc123",
		},
		LocationSource: job.LocationSourceRequest,
		Steps: []job.Step{{
			Name: "plan", Role: job.RoleTool, Phase: job.PhasePlan, Args: []string{"tofu", "plan"}, Env: nil,
			Dir: "", Timeout: 0, AllowFailure: false, ChangesResources: false, Outputs: nil,
		}},
		Purpose:   "",
		Pool:      "rpl-default",
		Profile:   "prf-default",
		Edits:     nil,
		Delivery:  "",
		Defaulted: nil,
	}
}

// dispatchOfFreezeJob returns the durable start request of the current phase of j.
func dispatchOfFreezeJob(j *job.Job) *durable.StartRequest {
	return &durable.StartRequest{
		Key: job.PhaseKey(j.ID, j.Current), Kind: job.RunnerKind(j.Spec.Pool), Version: 1, Input: nil,
	}
}

// freezeChange returns the change of kind ev on the job j at its version.
func freezeChange(j *job.Job, ev job.Event) job.Change {
	return job.Change{
		Job: j.ID, Version: j.Version, Event: ev, Wait: job.WaitNone, Reason: "", Dispatch: nil,
		PlanNotBefore: time.Time{}, Executor: "", Claim: nil, Delivery: nil, MergedAt: time.Time{}, Approval: nil,
		End: nil,
	}
}

// freezeEntry returns a valid audit entry for the freeze actions on unit.
func freezeEntry(unit identity.Object) audit.Entry {
	return audit.Entry{
		At: time.Now().UTC(), Namespace: unit.Namespace(), Subject: "al", IDPSubject: "", TokenID: "",
		Action: string(job.ActionNamespaceManageFreezes), Object: unit.String(), Version: "", ValuePath: "",
		ValueCount: nil, Changes: nil, SourceIP: "", UserAgent: "", Channel: audit.ChannelAPI, Kind: audit.KindChange,
		Outcome: audit.OutcomeSuccess, RequestID: "", ObjectID: "", PrevHash: "", Hash: "", Sequence: 0,
		FenceVersion: 0, Reason: "", PrincipalKind: "", JobID: "", Origin: nil,
	}
}

func TestFreezeReason(t *testing.T) {
	t.Parallel()

	if err := job.CheckReason("release freeze"); err != nil {
		t.Fatalf("CheckReason with a reason = %v", err)
	}

	if err := job.CheckReason(strings.Repeat("r", audit.MaxReasonLen)); err != nil {
		t.Fatalf("CheckReason with %d characters = %v", audit.MaxReasonLen, err)
	}

	for _, reason := range []string{"", strings.Repeat("r", audit.MaxReasonLen+1), "a\nb"} {
		if err := job.CheckReason(reason); !errors.Is(err, job.ErrInvalid) {
			t.Fatalf("CheckReason with %q = %v, want ErrInvalid", reason, err)
		}
	}

	if job.ErrNoActiveFreeze == nil {
		t.Fatal("ErrNoActiveFreeze is nil")
	}
}

func TestFreezeGate(t *testing.T) {
	t.Parallel()

	st, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := st.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	g := newFreezeGuard(t, st, "fzgate")
	now := time.Now()
	gate, err := job.NewFreezeGate(t.Context(), g.freezes, g.fence.Namespace, now)
	if err != nil {
		t.Fatalf("NewFreezeGate = %v", err)
	}

	active, found := gate.Active()
	if !found || active.ID != g.freeze.ID {
		t.Fatalf("Active = %s, %v; want %s", active.ID, found, g.freeze.ID)
	}

	applyErr := gate.Allow(job.PhaseApply, nil)
	if !errors.Is(applyErr, job.ErrFrozen) {
		t.Fatalf("Allow of a changing phase = %v, want ErrFrozen", applyErr)
	}

	planErr := gate.Allow(job.PhasePlan, nil)
	if planErr != nil {
		t.Fatalf("Allow of a plan = %v", planErr)
	}

	breakErr := gate.Allow(job.PhaseApply, &job.BreakGlass{Reason: "outage", Freeze: g.freeze.ID})
	if breakErr != nil {
		t.Fatalf("Allow of a break-glass = %v", breakErr)
	}

	emptyFence, _ := freezeFixture(t, st, "fzempty")
	emptyGate, err := job.NewFreezeGate(t.Context(), st.Jobs(), emptyFence.Namespace, now)
	if err != nil {
		t.Fatalf("NewFreezeGate with no freeze = %v", err)
	}

	if _, found := emptyGate.Active(); found {
		t.Fatal("Active of a namespace with no freeze is true")
	}

	if err := emptyGate.Allow(job.PhaseApply, nil); err != nil {
		t.Fatalf("Allow with no freeze = %v", err)
	}
}

func TestFreezeRaceBetweenCheckAndDispatch(t *testing.T) {
	t.Parallel()

	st, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := st.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	fence, unit := freezeFixture(t, st, "fzrace")
	stores := st.Jobs()
	gate, err := job.NewFreezeGate(t.Context(), stores, fence.Namespace, time.Now())
	if err != nil {
		t.Fatalf("NewFreezeGate = %v", err)
	}

	if _, found := gate.Active(); found {
		t.Fatal("Active is true before the freeze")
	}

	j := newFreezeJob(unit)

	if err := gate.Allow(j.Phases[0].Kind, nil); err != nil {
		t.Fatalf("Allow before the freeze = %v, want nil", err)
	}

	// A freeze starts between the gate read and the dispatch: the gate is
	// stale, so it still allows the phase, and only the store refuses it.
	fr := job.Freeze{
		ID: ident.NewID("frz-"), Namespace: fence.Namespace, Reason: "release week",
		StartsAt: time.Now().Add(-time.Hour), EndsAt: time.Time{}, CreatedBy: "al", EndedBy: "",
	}
	if err := stores.CreateFreeze(t.Context(), fr, fence, freezeEntry(unit)); err != nil {
		t.Fatalf("CreateFreeze = %v", err)
	}

	if err := gate.Allow(j.Phases[0].Kind, nil); err != nil {
		t.Fatalf("stale gate Allow after the freeze = %v, want nil (the gate holds one read)", err)
	}

	if _, _, err := stores.Create(t.Context(), *j, dispatchOfFreezeJob(j), fence, nil); !errors.Is(err, job.ErrFrozen) {
		t.Fatalf("Create after a freeze raced in = %v, want ErrFrozen", err)
	}
}

func TestFreezeGateWithoutStoreFailsClosed(t *testing.T) {
	t.Parallel()

	gate, err := job.NewFreezeGate(t.Context(), nil, "acme", time.Now())
	if !errors.Is(err, job.ErrNoFreezeStore) || gate != nil {
		t.Fatalf("NewFreezeGate with a nil store = %v, %v; want ErrNoFreezeStore and no gate", gate, err)
	}
}

// A freeze that starts leaves a job that already runs unchanged: a running
// job keeps running, and a queued job stays queued.
func TestFreezeStartKeepsRunningJobs(t *testing.T) {
	t.Parallel()

	st, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := st.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	fence, unit := freezeFixture(t, st, "fzrunning")
	stores := st.Jobs()

	running := newFreezeJob(unit)

	dispatched, _, err := stores.Create(t.Context(), *running, dispatchOfFreezeJob(running), fence, nil)
	if err != nil || dispatched.State != job.StateDispatched {
		t.Fatalf("Create before the freeze = %s, %v; want dispatched", dispatched.State, err)
	}

	queued := newFreezeJobOf(unit, "", job.OperationPlan)

	waiting, _, err := stores.Create(t.Context(), *queued, nil, fence, nil)
	if err != nil || waiting.State != job.StateQueued {
		t.Fatalf("Create of a queued job = %s, %v; want queued", waiting.State, err)
	}

	fr := job.Freeze{
		ID: ident.NewID("frz-"), Namespace: fence.Namespace, Reason: "release week",
		StartsAt: time.Now().Add(-time.Minute), EndsAt: time.Time{}, CreatedBy: "al", EndedBy: "",
	}
	if err = stores.CreateFreeze(t.Context(), fr, fence, freezeEntry(unit)); err != nil {
		t.Fatalf("CreateFreeze = %v", err)
	}

	for _, before := range []job.Job{dispatched, waiting} {
		after, getErr := stores.Get(t.Context(), before.ID)
		if getErr != nil || after.State != before.State || after.Version != before.Version {
			t.Fatalf("job %s after the freeze = %s v%d, %v; want %s v%d unchanged", before.ID, after.State,
				after.Version, getErr, before.State, before.Version)
		}
	}
}

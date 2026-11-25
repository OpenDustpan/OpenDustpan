package job_test

import (
	"errors"
	"path/filepath"
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
		{"cancel and phase dispatch are refused", refusesCancelAndDispatch},
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
	j := newFreezeJob(g.unit, "")

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
	j := newFreezeJob(g.unit, "")
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

	queued, _, err := g.jobs.Create(t.Context(), *newFreezeJob(g.unit, ""), nil, g.fence, nil)
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
}

// refusesSecondCreate checks that a second create of a freeze in a namespace
// with an active freeze is job.ErrFreezeActive.
func refusesSecondCreate(t *testing.T, st *sqlite.Store) {
	t.Helper()

	g := newFreezeGuard(t, st, "fzsecond")

	second := g.freeze
	second.ID = ident.NewID("frz-")

	err := g.freezes.CreateFreeze(t.Context(), second, g.fence, freezeEntry(g.unit))
	if !errors.Is(err, job.ErrFreezeActive) {
		t.Fatalf("second CreateFreeze = %v, want ErrFreezeActive", err)
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
func newFreezeJob(unit identity.Object, key string) *job.Job {
	return newFreezeJobOf(unit, key, job.OperationRefresh)
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

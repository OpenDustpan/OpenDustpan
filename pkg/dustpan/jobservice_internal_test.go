package dustpan

import (
	"context"
	"errors"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/runner"
)

var errJobStoreDown = errors.New("store: password=hunter2 refused")

func jobTestUnit(t *testing.T, ns, name string) identity.Object {
	t.Helper()

	u, err := identity.UnitObject("opentofu", namespace.Name(ns), name)
	if err != nil {
		t.Fatalf("UnitObject = %v", err)
	}

	return u
}

// inventoryEntry returns the entry of a unit in the inventory.
func inventoryEntry(name, unitID string) inventory.Entry {
	return inventory.Entry{
		LastChanged: time.Time{}, Namespace: "acme", Tool: "opentofu", Name: name, UnitID: unitID,
		DisplayName: "", Status: "", ResourceCount: nil, Lock: nil, ManualLock: nil, Collection: "", Labels: nil,
	}
}

// namespaceInventory lists the entries of the namespace that is asked, as the
// real reader does.
type namespaceInventory struct {
	fakeInventory
}

func (f namespaceInventory) ListUnits(_ context.Context, ns namespace.Name) ([]inventory.Entry, error) {
	var out []inventory.Entry

	for i := range f.entries {
		if f.entries[i].Namespace == ns {
			out = append(out, f.entries[i])
		}
	}

	return out, f.err
}

func inventoryOf(entries ...inventory.Entry) namespaceInventory {
	return namespaceInventory{fakeInventory{entries: entries, err: nil}}
}

// unitCase is one case of TestJobUnits.
type unitCase struct {
	name    string
	ref     identity.Object
	reader  namespaceInventory
	wantID  string
	wantErr error
}

func unitCases(t *testing.T) []unitCase {
	t.Helper()

	web := inventoryEntry("web", "ws-1")
	db := inventoryEntry("db", "ws-2")
	noID := inventoryEntry("web", "")

	return []unitCase{
		{"a live unit", jobTestUnit(t, "acme", "web"), inventoryOf(web), "ws-1", nil},
		// Defense in depth: the resolver drops the ID of a caller and finds its own.
		{
			"an ID from the caller is dropped",
			jobTestUnit(t, "acme", "web").WithUnitID("ws-evil"),
			inventoryOf(web),
			"ws-1",
			nil,
		},
		{
			"an ID of another unit does not find the other unit", jobTestUnit(t, "acme", "web").WithUnitID("ws-2"),
			inventoryOf(web, db), "ws-1", nil,
		},
		{"no such unit", jobTestUnit(t, "acme", "nope"), inventoryOf(web), "", job.ErrNotFound},
		{"a unit of another namespace", jobTestUnit(t, "other", "web"), inventoryOf(web), "", job.ErrNotFound},
		{"an entry with no ID", jobTestUnit(t, "acme", "web"), inventoryOf(noID), "", errUnitMismatch},
		{
			"the reader fails", jobTestUnit(t, "acme", "web"),
			namespaceInventory{fakeInventory{entries: nil, err: errReaderDown}},
			"", errReaderDown,
		},
	}
}

func TestJobUnits(t *testing.T) {
	t.Parallel()

	cases := unitCases(t)

	for i := range cases {
		tt := &cases[i]

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			u := jobUnits{facts: inventoryFacts{reader: tt.reader, ids: nil, deleted: nil}}
			got, err := u.Unit(t.Context(), tt.ref)

			if tt.wantErr != nil {
				// Only a missing unit is ErrNotFound: any other failure is not "no such unit".
				if !errors.Is(err, tt.wantErr) ||
					errors.Is(err, job.ErrNotFound) != errors.Is(tt.wantErr, job.ErrNotFound) {
					t.Fatalf("Unit = %v, want %v", err, tt.wantErr)
				}

				return
			}

			if err != nil || got.UnitID() != tt.wantID || got.UnitName() != "web" || got.Namespace() != "acme" ||
				got.Tool() != "opentofu" {
				t.Fatalf("Unit = %v, %v; want web with the ID %q", got, err, tt.wantID)
			}
		})
	}
}

// fakePools serves the pools of every namespace that is asked.
type fakePools struct {
	runner.PoolStore

	pools []runner.Pool
	err   error
}

func (f fakePools) Pools(context.Context, namespace.Name) ([]runner.Pool, error) {
	return f.pools, f.err
}

func poolsOf(pools ...runner.Pool) fakePools {
	return fakePools{PoolStore: nil, pools: pools, err: nil}
}

func testPool(id, ns, name string, purpose runner.Purpose) runner.Pool {
	return runner.Pool{
		ID: id, Namespace: namespace.Name(ns), Name: name, Purpose: purpose, Lease: 0, Concurrency: 0,
		Isolation: nil, GitCredentials: runner.GitCredentials{Mode: "", Helper: ""}, CreatedAt: time.Time{},
		UpdatedAt: time.Time{},
	}
}

// fakeConnections owns the connections in owned for the namespace acme.
type fakeConnections struct {
	owned []string
	err   error
}

func (f fakeConnections) Owns(_ context.Context, ns namespace.Name, id string) (bool, error) {
	return f.err == nil && ns == "acme" && slices.Contains(f.owned, id), f.err
}

// plannerCase is one case of TestJobPlanner. A nil change leaves the request
// as it is.
type plannerCase struct {
	name     string
	pools    fakePools
	conns    connectionOwner
	change   func(*job.StartRequest)
	wantErr  error
	wantPool string
}

func plannerRequest(unit identity.Object) job.StartRequest {
	return job.StartRequest{
		Unit: unit, Operation: job.OperationPlan, Purpose: "", Executor: "",
		Location: job.Location{Connection: "", Repository: "acme/infra", Ref: "main", Directory: "net", Revision: ""},
		Pool:     "", Edits: nil, Delivery: "", Source: "", IdempotencyKey: "", Reason: "", BreakGlass: nil,
		Channel: "", SourceIP: "", UserAgent: "", RequestID: "",
	}
}

func runPlannerCases(t *testing.T, cases []plannerCase) {
	t.Helper()

	for i := range cases {
		tt := &cases[i]

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			checkPlan(t, tt)
		})
	}
}

func checkPlan(t *testing.T, tt *plannerCase) {
	t.Helper()

	p := jobPlanner{pools: tt.pools, connections: tt.conns}
	unit := jobTestUnit(t, "acme", "web").WithUnitID("ws-1")
	req := plannerRequest(unit)

	if tt.change != nil {
		tt.change(&req)
	}

	spec, err := p.Plan(t.Context(), unit, &req)
	if tt.wantErr != nil {
		checkPlanRefusal(t, err, tt.wantErr)

		return
	}

	if err != nil {
		t.Fatalf("Plan = %v", err)
	}

	checkPlanSpec(t, &spec, &req, tt.wantPool)
}

func checkPlanRefusal(t *testing.T, err, want error) {
	t.Helper()

	if !errors.Is(err, want) {
		t.Fatalf("Plan = %v, want %v", err, want)
	}

	if errors.Is(err, identity.ErrDenied) && strings.Contains(err.Error(), "conn-1") {
		t.Fatalf("Plan = %v: the answer for a refused connection names the connection", err)
	}
}

func checkPlanSpec(t *testing.T, spec *job.Spec, req *job.StartRequest, wantPool string) {
	t.Helper()

	if spec.Pool != wantPool || spec.Unit.String() != req.Unit.String() || spec.Unit.UnitID() != "ws-1" ||
		spec.Operation != job.OperationPlan || spec.Executor != job.ExecutorRunner ||
		spec.Location != req.Location || len(spec.Steps) != 2 || spec.Profile != "" ||
		len(spec.Edits) != 0 || spec.Delivery != "" {
		t.Fatalf("spec = %+v, want a plan of web on pool %q at %+v", *spec, wantPool, req.Location)
	}
}

func TestJobPlannerPools(t *testing.T) {
	t.Parallel()

	ci := testPool("pool-1", "acme", "ci", runner.PurposeJobs)
	deploy := testPool("pool-2", "acme", "deploy", runner.PurposeJobs)
	sweeper := testPool("pool-s", "acme", "sweeper", runner.PurposeSweeps)
	foreign := testPool("pool-x", "other", "ci", runner.PurposeJobs)

	named := func(name string) func(*job.StartRequest) { return func(q *job.StartRequest) { q.Pool = name } }

	runPlannerCases(t, []plannerCase{
		{"the only job pool is the default", poolsOf(ci), nil, nil, nil, "pool-1"},
		{"a sweep pool is no default", poolsOf(ci, sweeper), nil, nil, nil, "pool-1"},
		{"a named pool", poolsOf(ci, deploy), nil, named("deploy"), nil, "pool-2"},
		{"zero pools", poolsOf(), nil, nil, job.ErrUnsupported, ""},
		{"only a sweep pool", poolsOf(sweeper), nil, nil, job.ErrUnsupported, ""},
		{"two job pools", poolsOf(ci, deploy), nil, nil, job.ErrUnsupported, ""},
		{"a pool of another namespace is no default", poolsOf(foreign), nil, nil, job.ErrUnsupported, ""},
		{"a named pool of another namespace", poolsOf(foreign), nil, named("ci"), job.ErrInvalid, ""},
		{"a named sweep pool", poolsOf(sweeper), nil, named("sweeper"), job.ErrInvalid, ""},
		{"an unknown pool name", poolsOf(ci), nil, named("nope"), job.ErrInvalid, ""},
		{
			"the store of pools fails",
			fakePools{PoolStore: nil, pools: nil, err: errJobStoreDown},
			nil,
			nil,
			errJobStoreDown,
			"",
		},
	})
}

func TestJobPlannerLocation(t *testing.T) {
	t.Parallel()

	pools := poolsOf(testPool("pool-1", "acme", "ci", runner.PurposeJobs))
	withConn := func(q *job.StartRequest) { q.Location.Connection = "conn-1" }
	owned := fakeConnections{owned: []string{"conn-1"}, err: nil}

	runPlannerCases(t, []plannerCase{
		{"no code location", pools, nil, func(q *job.StartRequest) {
			q.Location = job.Location{Connection: "", Repository: "", Ref: "", Directory: "", Revision: ""}
		}, job.ErrUnsupported, ""},
		{
			"a location that is not valid",
			pools,
			nil,
			func(q *job.StartRequest) { q.Location.Ref = "--upload-pack=x" },
			job.ErrInvalid,
			"",
		},
		{"a connection of the namespace", pools, owned, withConn, nil, "pool-1"},
		{
			"a connection of another namespace",
			pools,
			fakeConnections{owned: nil, err: nil},
			withConn,
			identity.ErrDenied,
			"",
		},
		{"a server with no connections", pools, nil, withConn, identity.ErrDenied, ""},
		{
			"the connection lookup fails",
			pools,
			fakeConnections{owned: nil, err: errJobStoreDown},
			withConn,
			errJobStoreDown,
			"",
		},
	})
}

func TestJobPlannerRequest(t *testing.T) {
	t.Parallel()

	pools := poolsOf(testPool("pool-1", "acme", "ci", runner.PurposeJobs))

	runPlannerCases(t, []plannerCase{
		{"apply", pools, nil, func(q *job.StartRequest) { q.Operation = job.OperationApply }, job.ErrUnsupported, ""},
		{
			"destroy",
			pools,
			nil,
			func(q *job.StartRequest) { q.Operation = job.OperationDestroy },
			job.ErrUnsupported,
			"",
		},
		{
			"write-back",
			pools,
			nil,
			func(q *job.StartRequest) { q.Executor = job.ExecutorWriteBack },
			job.ErrUnsupported,
			"",
		},
		{
			"the runner executor by name",
			pools,
			nil,
			func(q *job.StartRequest) { q.Executor = job.ExecutorRunner },
			nil,
			"pool-1",
		},
		{
			"edits", pools, nil,
			func(q *job.StartRequest) { q.Edits = []job.FileEdit{{Path: "main.tf", Keys: nil}} }, job.ErrInvalid, "",
		},
		{
			"a delivery",
			pools,
			nil,
			func(q *job.StartRequest) { q.Delivery = job.DeliveryDirectPush },
			job.ErrInvalid,
			"",
		},
	})
}

func TestPlanSteps(t *testing.T) {
	t.Parallel()

	steps, err := planSteps("opentofu")
	if err != nil || len(steps) != 2 {
		t.Fatalf("planSteps(opentofu) = %d steps, %v; want 2", len(steps), err)
	}

	checkOpenTofuSteps(t, steps)

	// A caller that changes one result must not change the next one.
	steps[1].Args[0] = "sh"
	steps[1].Env["TF_IN_AUTOMATION"] = "0"

	again, err := planSteps("opentofu")
	if err != nil || again[1].Args[0] != "tofu" || again[1].Env["TF_IN_AUTOMATION"] != "1" {
		t.Fatalf("planSteps after a change = %+v, %v; want fresh steps", again, err)
	}
}

func checkOpenTofuSteps(t *testing.T, steps []job.Step) {
	t.Helper()

	wantArgs := [][]string{{"tofu", "init", "-input=false"}, {"tofu", "plan", "-input=false", "-out=tfplan"}}
	for i := range steps {
		s := &steps[i]
		if !slices.Equal(s.Args, wantArgs[i]) || s.Role != job.RoleTool || s.Phase != job.PhasePlan ||
			s.ChangesResources || s.AllowFailure || s.Env["TF_IN_AUTOMATION"] != "1" {
			t.Errorf("step %d = %+v, want a plan-phase tool step with args %v that changes no resource", i, *s,
				wantArgs[i])
		}
	}

	checkStepNames(t, steps)
}

func checkStepNames(t *testing.T, steps []job.Step) {
	t.Helper()

	if steps[0].Name != "init" || steps[1].Name != "plan" {
		t.Errorf("step names = %q, %q; want init, plan", steps[0].Name, steps[1].Name)
	}

	if len(steps[0].Outputs) != 0 || len(steps[1].Outputs) != 1 ||
		steps[1].Outputs[0] != (job.Output{Kind: job.OutputPlanApply, Path: "tfplan"}) {
		t.Errorf("outputs = %+v, %+v; want the apply plan on the plan step only", steps[0].Outputs, steps[1].Outputs)
	}
}

func TestPlanStepsRefuseOtherTools(t *testing.T) {
	t.Parallel()

	for _, tool := range []string{"pulumi", "", "terraform", "OpenTofu", "opentofu "} {
		if _, err := planSteps(tool); !errors.Is(err, job.ErrUnsupported) {
			t.Errorf("planSteps(%q) = %v, want ErrUnsupported", tool, err)
		}
	}
}

// fakeSnapshots serves one configuration and records the scopes it was asked for.
type fakeSnapshots struct {
	cfg    gate.Config
	err    error
	chains [][]gate.Scope
}

func (f *fakeSnapshots) Snapshot(_ context.Context, scopes []gate.Scope) (gate.Config, error) {
	f.chains = append(f.chains, scopes)

	return f.cfg, f.err
}

func configOf(gates ...gate.Gate) gate.Config {
	return gate.Config{
		Gates: gates, AccessLimits: nil, ApprovalRules: nil, OwnershipRules: nil, PathSets: nil,
		ApprovalDefaults: nil, CarryOver: nil,
	}
}

func snapshotsOf(gates ...gate.Gate) *fakeSnapshots {
	return &fakeSnapshots{cfg: configOf(gates...), err: nil, chains: nil}
}

func testGate(scope gate.Scope, id string, point gate.DecisionPoint, mode gate.Mode) gate.Gate {
	return gate.Gate{
		Scope: scope, ID: gate.ID(id), Version: 1, DecisionPoint: point, Condition: "", Unless: "",
		Outcome: gate.OutcomeDeny, Message: "", Mode: mode, OnError: "", Final: false,
		CheckedWith: gate.CheckedWith{Engine: "", Schema: gate.SchemaVersion{Subject: "", Major: 0, Minor: 0}},
		State:       "", Reads: nil,
	}
}

// gateCase is one case of TestJobGates.
type gateCase struct {
	name    string
	snaps   *fakeSnapshots
	reader  namespaceInventory
	want    []job.EnforcedGate
	wantErr error
}

func gateCases(t *testing.T) []gateCase {
	t.Helper()

	ns := gate.NamespaceScope("acme")

	unitScope, err := gate.UnitScope("acme", "opentofu", "web")
	if err != nil {
		t.Fatalf("UnitScope = %v", err)
	}

	live := inventoryOf(gateTestEntry())
	enforced := func(scope gate.Scope, id, point string) job.EnforcedGate {
		return job.EnforcedGate{Scope: scope.Key(), ID: id, Point: point}
	}

	return []gateCase{
		{"no gate", snapshotsOf(), live, nil, nil},
		{
			"an enforced gate at state.write",
			snapshotsOf(testGate(unitScope, "no-plans", gate.PointStateWrite, gate.ModeEnforce)), live,
			[]job.EnforcedGate{enforced(unitScope, "no-plans", "state.write")},
			nil,
		},
		{
			"an enforced gate at change.propose",
			snapshotsOf(testGate(ns, "review", gate.PointChangePropose, gate.ModeEnforce)), live,
			[]job.EnforcedGate{enforced(ns, "review", "change.propose")},
			nil,
		},
		{
			"a gate in audit mode",
			snapshotsOf(
				testGate(unitScope, "watch", gate.PointStateWrite, gate.ModeEvaluate),
				testGate(ns, "watch-propose", gate.PointChangePropose, gate.ModeEvaluate),
			), live, nil, nil,
		},
		{
			// Fail closed: a mode that this code does not know counts as enforced.
			"a gate with an unknown mode", snapshotsOf(testGate(ns, "odd", gate.PointStateWrite, gate.Mode(""))), live,
			[]job.EnforcedGate{enforced(ns, "odd", "state.write")},
			nil,
		},
		{
			"a gate at another decision point",
			snapshotsOf(
				testGate(ns, "later", gate.PointChangeApply, gate.ModeEnforce),
				testGate(ns, "planned", gate.PointChangePlanned, gate.ModeEnforce),
			), live, nil, nil,
		},
		{
			"one gate of each kind",
			snapshotsOf(
				testGate(ns, "a", gate.PointStateWrite, gate.ModeEvaluate),
				testGate(ns, "b", gate.PointStateWrite, gate.ModeEnforce),
				testGate(unitScope, "c", gate.PointChangePropose, gate.ModeEnforce),
			), live,
			[]job.EnforcedGate{enforced(ns, "b", "state.write"), enforced(unitScope, "c", "change.propose")},
			nil,
		},
		{
			"the store fails", &fakeSnapshots{cfg: configOf(), err: errJobStoreDown, chains: nil}, live, nil,
			errJobStoreDown,
		},
		{
			"the inventory fails", snapshotsOf(),
			namespaceInventory{fakeInventory{entries: nil, err: errReaderDown}},
			nil, errReaderDown,
		},
	}
}

func TestJobGates(t *testing.T) {
	t.Parallel()

	for _, tt := range gateCases(t) {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			g := jobGates{snapshots: tt.snaps, members: inventoryFacts{reader: tt.reader, ids: nil, deleted: nil}}

			got, err := g.Enforced(t.Context(), jobTestUnit(t, "acme", "web"))
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("Enforced = %v, want %v", err, tt.wantErr)
				}

				return
			}

			if err != nil || !slices.Equal(got, tt.want) {
				t.Fatalf("Enforced = %+v, %v; want %+v", got, err, tt.want)
			}
		})
	}
}

// scopeKeys returns the keys of the scopes that the first snapshot asked for.
func scopeKeys(snaps *fakeSnapshots) []string {
	keys := make([]string, 0, len(snaps.chains[0]))
	for _, s := range snaps.chains[0] {
		keys = append(keys, s.Key())
	}

	return keys
}

// TestJobGatesAskForTheWholeChain proves the snapshot covers the installation,
// the namespace, the collection of the unit and the unit.
func TestJobGatesAskForTheWholeChain(t *testing.T) {
	t.Parallel()

	snaps := snapshotsOf()
	g := jobGates{
		snapshots: snaps,
		members:   inventoryFacts{reader: inventoryOf(gateTestEntry()), ids: nil, deleted: nil},
	}

	if _, err := g.Enforced(t.Context(), jobTestUnit(t, "acme", "web")); err != nil {
		t.Fatalf("Enforced = %v", err)
	}

	want := []string{"installation", "namespace/acme", "collection/acme/prod", "unit/acme/opentofu/web"}
	if keys := scopeKeys(snaps); !slices.Equal(keys, want) {
		t.Fatalf("scopes = %v, want %v", keys, want)
	}

	// A unit in no collection has the shorter chain.
	snaps = snapshotsOf()
	g = jobGates{
		snapshots: snaps,
		members: inventoryFacts{
			reader:  inventoryOf(inventoryEntry("web", "ws-1")),
			ids:     nil,
			deleted: nil,
		},
	}

	if _, err := g.Enforced(t.Context(), jobTestUnit(t, "acme", "web")); err != nil || len(snaps.chains[0]) != 3 {
		t.Fatalf("Enforced = %v, scopes %d; want 3 scopes for a unit in no collection", err, len(snaps.chains[0]))
	}
}

// recordAuthorizer records the calls and answers with err.
type recordAuthorizer struct {
	calls []string
	err   error
}

func (a *recordAuthorizer) Authorize(_ context.Context, _ identity.Principal, action identity.Action,
	obj identity.Object,
) error {
	a.calls = append(a.calls, string(action)+" "+obj.String())

	return a.err
}

func (*recordAuthorizer) ListObjects(context.Context, identity.Principal, identity.Action) (identity.Scope, error) {
	return identity.Scope{Namespaces: nil, Collections: nil, Units: nil, Principals: nil, All: true}, nil
}

func adaPrincipal() identity.Principal {
	return identity.Principal{
		Subject: "ada", DisplayName: "", Admin: false, TokenID: "", TokenExpiresAt: time.Time{},
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{}, Kind: identity.KindPerson, Credential: "",
		JobID: "",
	}
}

// TestUnitLimits proves an action on a unit goes through the access limits,
// and an action on a namespace goes through the grants alone.
func TestUnitLimits(t *testing.T) {
	t.Parallel()

	grants := &recordAuthorizer{calls: nil, err: nil}
	limited := &recordAuthorizer{calls: nil, err: identity.ErrDenied}
	a := unitLimits{grants: grants, limited: limited}

	unit := jobTestUnit(t, "acme", "web")

	err := a.Authorize(t.Context(), adaPrincipal(), identity.ActionUnitRunPlan, unit)
	if !errors.Is(err, identity.ErrDenied) || len(limited.calls) != 1 || len(grants.calls) != 0 {
		t.Fatalf("unit action = %v, limited %v, grants %v; want a refusal by the limits", err, limited.calls,
			grants.calls)
	}

	err = a.Authorize(t.Context(), adaPrincipal(), identity.ActionNamespaceManageRunners,
		identity.NamespaceObject("acme"))
	if err != nil || len(grants.calls) != 1 || len(limited.calls) != 1 {
		t.Fatalf("namespace action = %v, grants %v; want the grants alone to decide", err, grants.calls)
	}
}

// TestNewJobServiceNeedsTheGateService proves the server starts no job when it
// lacks a port, and first of all the gate service that checks the limits.
func TestNewJobServiceNeedsTheGateService(t *testing.T) {
	t.Parallel()

	var svc services

	starter, err := newJobService(svc, new(settings))
	if starter != nil || err != nil {
		t.Fatalf("newJobService with no port = %v, %v; want no service", starter, err)
	}
}

func driftRequest(unit identity.Object) job.StartRequest {
	req := plannerRequest(unit)
	req.Operation, req.Purpose = job.OperationRefresh, job.PurposeDriftCheck

	return req
}

func TestJobPlannerDriftCheck(t *testing.T) {
	t.Parallel()

	pools := poolsOf(testPool("pool-1", "acme", "ci", runner.PurposeJobs))
	p := jobPlanner{pools: pools, connections: nil}
	unit := jobTestUnit(t, "acme", "web").WithUnitID("ws-1")

	spec, err := p.Plan(t.Context(), unit, new(driftRequest(unit)))
	if err != nil {
		t.Fatalf("Plan = %v", err)
	}

	if spec.Operation != job.OperationRefresh || spec.Purpose != job.PurposeDriftCheck ||
		spec.Executor != job.ExecutorRunner || spec.Pool != "pool-1" || spec.Changes() || len(spec.Steps) != 2 {
		t.Fatalf("spec = %+v, want a drift check of web on pool-1 with two steps that changes nothing", spec)
	}

	// The runner executor accepts the steps of the planner, and the phase list
	// is the plan phase alone.
	if validateErr := job.ValidateDriftSteps(spec.Steps); validateErr != nil {
		t.Fatalf("ValidateDriftSteps of the planned steps = %v", validateErr)
	}

	kinds, err := job.PhasesFor(spec.Executor, spec.Operation, spec.Purpose)
	if err != nil || !slices.Equal(kinds, []job.PhaseKind{job.PhasePlan}) {
		t.Fatalf("PhasesFor = %v, %v; want the plan phase", kinds, err)
	}
}

func TestJobPlannerDriftCheckRefusals(t *testing.T) {
	t.Parallel()

	pools := poolsOf(testPool("pool-1", "acme", "ci", runner.PurposeJobs))

	tests := []struct {
		name   string
		change func(*job.StartRequest)
		want   error
	}{
		{"a drift check of a plan", func(q *job.StartRequest) { q.Operation = job.OperationPlan }, job.ErrInvalid},
		{"a drift check of an apply", func(q *job.StartRequest) { q.Operation = job.OperationApply }, job.ErrInvalid},
		{"a refresh with no purpose", func(q *job.StartRequest) { q.Purpose = job.PurposeNone }, job.ErrUnsupported},
		{"write-back", func(q *job.StartRequest) { q.Executor = job.ExecutorWriteBack }, job.ErrUnsupported},
		{"edits", func(q *job.StartRequest) { q.Edits = []job.FileEdit{{Path: "a.json", Keys: nil}} }, job.ErrInvalid},
		{"a delivery", func(q *job.StartRequest) { q.Delivery = job.DeliveryDirectPush }, job.ErrInvalid},
		{
			"no code location",
			func(q *job.StartRequest) {
				q.Location = job.Location{Connection: "", Repository: "", Ref: "", Directory: "", Revision: ""}
			},
			job.ErrUnsupported,
		},
		{"a tool with no refresh-only plan", nil, job.ErrUnsupported},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			p := jobPlanner{pools: pools, connections: nil}
			unit := jobTestUnit(t, "acme", "web").WithUnitID("ws-1")
			req := driftRequest(unit)

			if tt.change != nil {
				tt.change(&req)
			} else {
				pulumi, err := identity.UnitObject("pulumi", "acme", "proj/stack")
				if err != nil {
					t.Fatalf("unit object: %v", err)
				}

				unit, req.Unit = pulumi.WithUnitID("stk-1"), pulumi
			}

			if _, err := p.Plan(t.Context(), unit, &req); !errors.Is(err, tt.want) {
				t.Fatalf("Plan = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestDriftSteps(t *testing.T) {
	t.Parallel()

	steps, err := driftSteps(opentofuTool)
	if err != nil || len(steps) != 2 {
		t.Fatalf("driftSteps(opentofu) = %d steps, %v; want 2", len(steps), err)
	}

	want := [][]string{
		{"tofu", "init", "-input=false"},
		{"tofu", "plan", "-refresh-only", "-input=false", "-out=drift.tfplan"},
	}

	for i := range steps {
		checkDriftStep(t, i, &steps[i], want[i])
	}

	// A caller that changes one result must not change the next one.
	steps[1].Args[0] = "sh"
	steps[1].Env["TF_CLI_ARGS"] = "-lock=false"

	again, err := driftSteps(opentofuTool)
	if err != nil || again[1].Args[0] != "tofu" || len(again[1].Env) != 1 {
		t.Fatalf("driftSteps after a change = %+v, %v; want fresh steps", again, err)
	}

	for _, tool := range []string{"pulumi", "", "OpenTofu"} {
		if _, toolErr := driftSteps(tool); !errors.Is(toolErr, job.ErrUnsupported) {
			t.Errorf("driftSteps(%q) = %v, want ErrUnsupported", tool, toolErr)
		}
	}
}

// checkDriftStep checks that step i is a plan-phase tool step with the
// arguments want that changes nothing, sets one variable and leaves no output.
func checkDriftStep(t *testing.T, i int, s *job.Step, want []string) {
	t.Helper()

	// No step may carry -lock=false: a check keeps the default state lock.
	if !slices.Equal(s.Args, want) || slices.Contains(s.Args, "-lock=false") {
		t.Errorf("step %d args = %v, want %v with the default state lock", i, s.Args, want)
	}

	if s.Role != job.RoleTool || s.Phase != job.PhasePlan || s.ChangesResources || s.AllowFailure ||
		len(s.Outputs) != 0 {
		t.Errorf("step %d = %+v, want a plan-phase tool step that changes nothing and leaves no output", i, *s)
	}

	if !maps.Equal(s.Env, map[string]string{"TF_IN_AUTOMATION": "1"}) {
		t.Errorf("step %d env = %v, want TF_IN_AUTOMATION alone", i, s.Env)
	}
}

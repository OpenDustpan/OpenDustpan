package runnerapi_test

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/job/jobtest"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// unitOf makes the OpenTofu workspace name in the namespace of fence and
// returns its unit reference with the stable unit ID.
func unitOf(t *testing.T, s stores, fence namespace.Fence, name string) identity.Object {
	t.Helper()

	ref, err := workspace.ParseName(name)
	if err != nil {
		t.Fatalf("workspace name: %v", err)
	}

	now := time.UnixMilli(1_700_000_000_000).UTC()

	w := workspace.Workspace{
		NamespaceID: "",
		CreatedAt:   now,
		UpdatedAt:   now,
		Lock:        workspace.Lock{LockedAt: time.Time{}, ID: "", Holder: "", Reason: ""},
		LastRelease: workspace.NoRelease(),
		ID:          workspace.ID(ident.NewID("ws-")),
		Ref:         workspace.Ref{Namespace: fence.Namespace, Name: ref},
		Revision:    1,
		Overrides:   namespace.Overrides{Forced: false, Unlocked: false},
		Origin:      identity.StateOrigin{Job: "", Commit: ""},
	}
	if err = s.units.CreateWorkspace(t.Context(), w, fence); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	entries, err := s.units.ListUnits(t.Context(), fence.Namespace)
	if err != nil {
		t.Fatalf("list units: %v", err)
	}

	for i := range entries {
		if entries[i].Tool == "opentofu" && entries[i].Name == name {
			unit, unitErr := identity.UnitObject("opentofu", fence.Namespace, name)
			if unitErr != nil {
				t.Fatalf("unit object: %v", unitErr)
			}

			return unit.WithUnitID(entries[i].UnitID)
		}
	}

	t.Fatalf("unit %s is not in the inventory", name)

	return identity.Object{}
}

// dispatchPlan stores a plan job of the pool on unit, with its phase
// dispatched as a durable operation of the kind of the pool.
func dispatchPlan(t *testing.T, e *env, pool string, unit identity.Object) job.Job {
	t.Helper()

	j := job.Job{
		ID:          job.ID(ident.NewID(job.IDPrefix)),
		NamespaceID: "",
		Spec: job.Spec{
			Unit: unit, Operation: job.OperationPlan, Purpose: "", Executor: job.ExecutorRunner,
			Location: job.Location{
				Connection: "github", Repository: "acme/infra", Ref: "main", Directory: "network", Revision: "abc123",
			},
			LocationSource: job.LocationSourceRequest,
			Steps: []job.Step{{
				Name: "plan", Role: job.RoleTool, Phase: job.PhasePlan, Args: []string{"tofu", "plan"}, Env: nil,
				Dir: "", Timeout: 0, AllowFailure: false, ChangesResources: false, Outputs: nil,
			}},
			Pool: pool, Profile: "prf-default", Edits: nil, Delivery: "",
			Defaulted: nil,
		},
		State:  job.StateQueued,
		Wait:   job.WaitNone,
		Reason: "",
		Phases: []job.Phase{{
			Kind: job.PhasePlan, Operation: "", Claims: 0, Executor: "", Steps: nil, StateVersion: "", Plan: nil,
			Gates: nil, Approvals: nil, StartedAt: time.Time{}, FinishedAt: time.Time{},
		}},
		Current:        0,
		StartedBy:      "ada",
		Source:         job.SourceAPI,
		Agent:          "",
		AgentMode:      "",
		BreakGlass:     nil,
		Trigger:        nil,
		Delivery:       nil,
		Closed:         nil,
		Verification:   job.VerificationNone,
		IdempotencyKey: "",
		RequestHash:    "",
		Version:        0,
		CreatedAt:      time.Time{},
		UpdatedAt:      time.Time{},
		StateChangedAt: time.Time{},
	}

	start := &durable.StartRequest{Key: job.PhaseKey(j.ID, 0), Kind: job.RunnerKind(pool), Version: 1, Input: nil}

	stored, created, err := e.store.jobs.Create(t.Context(), j, start, e.fence, nil)
	if err != nil || !created {
		t.Fatalf("create job = created %v, %v; want a dispatched job", created, err)
	}

	return stored
}

// TestPollGetsThePhaseOfAJob proves that a runner poll finds the phase of a
// real job through the job claimer on the SQL stores: the claimer moves the
// job to running, the work names the job and its phase, and a second poll
// finds nothing.
func TestPollGetsThePhaseOfAJob(t *testing.T) {
	t.Parallel()

	for _, b := range backends() {
		t.Run(b.name, func(t *testing.T) {
			t.Parallel()

			pollGetsPhase(t, b.open)
		})
	}
}

func pollGetsPhase(t *testing.T, open func(t *testing.T) stores) {
	t.Helper()

	e := newJobEnv(t, open)
	pool := e.createPool()
	r := e.newRunner("builder-1")

	if status, body := r.join(pool, e.joinToken(pool)); status != http.StatusOK {
		t.Fatalf("join = %d %s, want 200", status, body)
	}

	if work := r.poll(0); work != nil {
		t.Fatalf("poll with no job = %+v, want 204", work)
	}

	j := dispatchPlan(t, e, pool, unitOf(t, e.store, e.fence, "network"))

	work := r.poll(2 * time.Second)
	if work == nil || work.Job != string(j.ID) || work.Phase != 0 || work.Token != 1 ||
		work.Operation != j.Phases[0].Operation {
		t.Fatalf("poll = %+v, want phase 0 of job %s with token 1 on operation %s", work, j.ID, j.Phases[0].Operation)
	}

	checkRunning(t, e, j.ID, r.id)

	if again := r.poll(0); again != nil {
		t.Fatalf("second poll = %+v, want 204: the phase has a claim", again)
	}
}

// checkRunning checks that the job id runs its first phase, on the first
// claim of the runner.
func checkRunning(t *testing.T, e *env, id job.ID, runnerID string) {
	t.Helper()

	got, err := e.store.jobs.Get(t.Context(), id)
	if err != nil || got.State != job.StateRunning || got.Phases[0].Claims != 1 || got.Phases[0].Executor != runnerID {
		t.Fatalf("job after the poll = %s, %d claims, executor %q, %v; want running with 1 claim of runner %s",
			got.State, got.Phases[0].Claims, got.Phases[0].Executor, err, runnerID)
	}
}

// newJobEnv is newEnvOn with the real job claimer in place of the claimer of
// the other tests.
func newJobEnv(t *testing.T, open func(t *testing.T) stores) *env {
	t.Helper()

	return newEnvWith(t, open, func(s stores) job.Claimer {
		return job.NewClaimer(s.jobs, s.operations, s.namespaces, jobtest.NoGates{})
	})
}

// fixedGates is a job.GateGuard that answers with the gates that a case sets.
type fixedGates struct {
	mu       sync.Mutex
	enforced []job.EnforcedGate
}

// Enforced implements job.GateGuard.
func (g *fixedGates) Enforced(context.Context, identity.Object) ([]job.EnforcedGate, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	return g.enforced, nil
}

// set makes enforced the next answer.
func (g *fixedGates) set(enforced []job.EnforcedGate) {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.enforced = enforced
}

// TestPollMeetsTheGatesOfTheUnit proves, through the runner protocol, that a
// gate saved after a job was dispatched stops the claim of its phase: the
// poll finds no work, the job fails with the name of the gate, and the phase
// has no claim. The poll after the gate is gone takes the next job.
func TestPollMeetsTheGatesOfTheUnit(t *testing.T) {
	t.Parallel()

	for _, b := range backends() {
		t.Run(b.name, func(t *testing.T) {
			t.Parallel()

			pollMeetsGates(t, b.open)
		})
	}
}

func pollMeetsGates(t *testing.T, open func(t *testing.T) stores) {
	t.Helper()

	guard := &fixedGates{mu: sync.Mutex{}, enforced: nil}
	e := newEnvWith(t, open, func(s stores) job.Claimer {
		return job.NewClaimer(s.jobs, s.operations, s.namespaces, guard)
	})
	pool := e.createPool()
	r := e.newRunner("builder-1")

	if status, body := r.join(pool, e.joinToken(pool)); status != http.StatusOK {
		t.Fatalf("join = %d %s, want 200", status, body)
	}

	unit := unitOf(t, e.store, e.fence, "network")
	j := dispatchPlan(t, e, pool, unit)

	// The gate comes after the dispatch, before the poll.
	guard.set([]job.EnforcedGate{{Scope: "unit", ID: "no-plans", Point: "state.write"}})

	if work := r.poll(0); work != nil {
		t.Fatalf("poll with an enforced gate = %+v, want 204", work)
	}

	got, err := e.store.jobs.Get(t.Context(), j.ID)
	if err != nil || got.State != job.StateFailed || got.Phases[0].Claims != 0 ||
		!strings.Contains(got.Reason, "no-plans at state.write") {
		t.Fatalf("job after the refused poll = %s, %d claims, reason %q, %v; want failed by the gate no-plans",
			got.State, got.Phases[0].Claims, got.Reason, err)
	}

	guard.set(nil)

	next := dispatchPlan(t, e, pool, unit)

	if work := r.poll(2 * time.Second); work == nil || work.Job != string(next.ID) {
		t.Fatalf("poll with no gate = %+v, want the phase of job %s", work, next.ID)
	}
}

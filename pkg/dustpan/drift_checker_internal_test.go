package dustpan

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/driftsvc"
	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/drift/drifttest"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/manuallock"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/runner"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/store/postgres"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// TestDriftCheckerConformance runs the cases of drift.Checker of the drift
// suite on the drift service over the job service of the server, as the
// server wires them, on a SQLite store and on a PostgreSQL store. The suite
// skips the cases of Store and PolicyStore here: the store adapters run those.
func TestDriftCheckerConformance(t *testing.T) {
	t.Parallel()

	t.Run("sqlite", func(t *testing.T) {
		t.Parallel()

		drifttest.Run(t, func(t *testing.T) drifttest.Harness {
			t.Helper()

			st := openCollectionStore(t)

			return newDriftCheckerHarness(t, checkerBackend{store: st, jobs: st.Jobs(), runners: st.Runners()})
		})
	})

	t.Run("postgres", func(t *testing.T) {
		t.Parallel()

		base := os.Getenv(groupPostgresEnv)
		if base == "" {
			t.Skipf("set %s to run the PostgreSQL case", groupPostgresEnv)
		}

		drifttest.Run(t, func(t *testing.T) drifttest.Harness {
			t.Helper()

			st, err := postgres.Open(t.Context(), newGroupSchema(t, base))
			if err != nil {
				t.Fatalf("open store: %v", err)
			}

			t.Cleanup(st.Close)

			return newDriftCheckerHarness(t, checkerBackend{store: st, jobs: st.Jobs(), runners: st.Runners()})
		})
	})
}

// checkerStore is what the drift checker harness needs of a store. Both SQL
// adapters implement it.
type checkerStore interface {
	namespace.Store
	inventory.Reader
	CreateStack(ctx context.Context, s stack.Stack, fence namespace.Fence) error
	CreateWorkspace(ctx context.Context, w workspace.Workspace, fence namespace.Fence) error
	PutLock(ctx context.Context, l manuallock.Lock, fence namespace.Fence, e audit.Entry) error
}

// checkerBackend is one store with its job store and its runner stores.
type checkerBackend struct {
	store   checkerStore
	jobs    job.Store
	runners RunnerStores
}

// testClock is a clock that a case sets.
type testClock struct {
	mu  sync.Mutex
	now time.Time
}

// Now returns the time that the case set.
func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.now
}

// Set sets the time.
func (c *testClock) Set(at time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.now = at
}

// newDriftCheckerHarness returns a harness with the drift service of the server
// over the fresh store of b with the namespace acme and one runner pool of acme
// that runs jobs.
func newDriftCheckerHarness(t *testing.T, b checkerBackend) drifttest.Harness {
	t.Helper()

	created := time.Unix(0, 0).UTC()
	clock := &testClock{mu: sync.Mutex{}, now: created}

	st := b.store
	ns := createCollectionNamespace(t, st, created)
	createJobPool(t, b.runners, ns, created)

	checker := newDriftChecker(t, b, clock.Now)

	return drifttest.Harness{
		Store: nil, Policies: nil, Checker: checker,
		Checks: drifttest.CheckerFixture{
			Unit: func(t *testing.T, tool, name string) identity.Object {
				t.Helper()

				return seedUnit(t, st, ns, tool, name)
			},
			Actor: func(*testing.T) job.Actor { return driftActor() },
			Job: func(t *testing.T, id job.ID) job.Job {
				t.Helper()

				j, err := b.jobs.Get(t.Context(), id)
				if err != nil {
					t.Fatalf("read job %s: %v", id, err)
				}

				return j
			},
			Lock: func(t *testing.T, u identity.Object) {
				t.Helper()

				lockUnit(t, st, ns, u, created)
			},
			SetNow: func(_ *testing.T, at time.Time) { clock.Set(at) },
		},
		Namespace: nil, Jobs: drifttest.Jobs{
			DriftCheck: nil, Running: nil, Failed: nil, Canceled: nil, Plain: nil,
		},
		Projection: nil, Notified: nil, Audited: nil,
	}
}

// newDriftChecker builds the job service and the drift service as the server
// does, over the stores of b, with the clock now.
func newDriftChecker(t *testing.T, b checkerBackend, now func() time.Time) *driftsvc.Service {
	t.Helper()

	cfg := defaultSettings()
	cfg.namespaceStore = b.store
	cfg.jobStore = b.jobs
	cfg.inventoryReader = b.store
	cfg.runners = b.runners
	cfg.authorizer = identity.AdminOnly{}
	cfg.now = now

	namespaces, err := namespace.NewService(namespace.Config{Store: b.store, Now: now, Installation: "eu-west"})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	_, gates, err := newCollectionAndGateServices(cfg, namespaces)
	if err != nil || gates.svc == nil {
		t.Fatalf("gate service: %v, %v", gates.svc, err)
	}

	// The job service reads the namespace service and the gate service only.
	svc := services{
		namespaces:  namespaces,
		stacks:      nil,
		updates:     nil,
		secrets:     nil,
		workspaces:  nil,
		manualLocks: nil,
		tokens:      nil,
		collections: nil,
		placements:  nil,
		gates:       gates.svc,
		stateWrite:  gates,
		deleted:     nil,
		access:      nil,
		tempAdmins:  nil,
		issuer:      nil,
	}

	starter, err := newJobService(svc, cfg)
	if err != nil || starter == nil {
		t.Fatalf("job service: %v, %v", starter, err)
	}

	checker, err := driftsvc.NewService(starter, now)
	if err != nil {
		t.Fatalf("drift service: %v", err)
	}

	return checker
}

// driftActor returns an administrator who starts a drift check.
func driftActor() job.Actor {
	return job.Actor{
		Principal: identity.Principal{
			TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{}, Subject: "admin", DisplayName: "",
			Admin: true, TokenID: "", TokenExpiresAt: time.Time{}, Kind: "", Credential: "",
			JobID: "",
		},
		Agent: "", Mode: "", ConfirmAuth: "", AuthAt: time.Time{},
	}
}

// createJobPool stores the runner pool prod of ns, which runs jobs.
func createJobPool(t *testing.T, pools runner.PoolStore, ns namespace.Namespace, at time.Time) {
	t.Helper()

	p := runner.Pool{
		ID: "", Namespace: ns.Name, Name: "prod", Purpose: runner.PurposeJobs, Lease: 0, Concurrency: 0,
		Isolation: nil, GitCredentials: runner.GitCredentials{Mode: "", Helper: ""}, CreatedAt: at, UpdatedAt: at,
	}

	if _, err := pools.CreatePool(t.Context(), p, ns.Fence(), nil); err != nil {
		t.Fatalf("create runner pool: %v", err)
	}
}

// seedUnit stores the unit name of tool in ns and returns its reference with
// no unit ID: an OpenTofu workspace, or a Pulumi stack.
func seedUnit(t *testing.T, st checkerStore, ns namespace.Namespace, tool, name string) identity.Object {
	t.Helper()

	if tool == "pulumi" {
		u := seedStack(t, st, ns, name, "stk-"+name[:3])

		bare, err := identity.UnitObject(u.Tool(), u.Namespace(), u.UnitName())
		if err != nil {
			t.Fatalf("unit object: %v", err)
		}

		return bare
	}

	wsName, err := workspace.ParseName(name)
	if err != nil {
		t.Fatalf("workspace name: %v", err)
	}

	w := workspace.Workspace{
		CreatedAt:   ns.CreatedAt,
		UpdatedAt:   ns.CreatedAt,
		Lock:        workspace.Lock{LockedAt: time.Time{}, ID: "", Holder: "", Reason: ""},
		LastRelease: workspace.NoRelease(),
		ID:          workspace.ID(ident.NewID("ws-")),
		Ref:         workspace.Ref{Namespace: ns.Name, Name: wsName},
		NamespaceID: ns.ID,
		Overrides:   namespace.Overrides{Forced: false, Unlocked: false},
		Revision:    1,
		Origin:      identity.StateOrigin{Job: "", Commit: ""},
	}
	if createErr := st.CreateWorkspace(t.Context(), w, ns.Fence()); createErr != nil {
		t.Fatalf("create workspace %q: %v", name, createErr)
	}

	u, err := identity.UnitObject(tool, ns.Name, name)
	if err != nil {
		t.Fatalf("unit object: %v", err)
	}

	return u
}

// lockUnit puts a manual lock of admin on the unit u of ns.
func lockUnit(t *testing.T, st checkerStore, ns namespace.Namespace, u identity.Object, at time.Time) {
	t.Helper()

	l := manuallock.Lock{
		Since: at, Holder: "admin", Reason: "freeze",
		Target: manuallock.UnitTarget(collection.UnitRef{Namespace: ns.Name, Tool: u.Tool(), Name: u.UnitName()}),
	}

	if err := st.PutLock(t.Context(), l, ns.Fence(), nsChange(at, ns.Name, "lock:"+u.UnitName())); err != nil {
		t.Fatalf("lock unit %s: %v", u, err)
	}
}

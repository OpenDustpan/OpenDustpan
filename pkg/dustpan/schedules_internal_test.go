package dustpan

import (
	"context"
	"encoding/json"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/internal/localusersvc"
	"opendustpan.org/opendustpan/internal/metrics"
	"opendustpan.org/opendustpan/internal/scopeaudit"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/audit"
	blobfs "opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/session"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// scheduleTestEvery is the tick of the schedule tests: short enough for a
// fast test, long enough for the store clock to tell ticks apart.
const scheduleTestEvery = 50 * time.Millisecond

// scheduleTestDeadline is how long each schedule test waits for two runs.
const scheduleTestDeadline = 10 * time.Second

// durableRunnerPair holds the scheduler and the runner of one instance.
type durableRunnerPair struct {
	Sched *durable.Scheduler
	Run   *durable.Runner
}

// newPair builds the scheduler and the runner of srv.
func newPair(t *testing.T, srv *Server) durableRunnerPair {
	t.Helper()

	sched, runner, err := srv.newJobRunner()
	if err != nil {
		t.Fatalf("job runner: %v", err)
	}

	if sched == nil || runner == nil {
		t.Fatal("job runner is nil with jobs enabled")
	}

	return durableRunnerPair{Sched: sched, Run: runner}
}

// listOps returns the operations of kind that the store holds.
func listOps(t *testing.T, srv *Server, kind durable.Kind) []durable.Operation {
	t.Helper()

	ops, err := srv.durable.List(t.Context(), durable.Filter{Kind: kind, States: nil, Limit: 0})
	if err != nil {
		t.Fatalf("list operations of %s: %v", kind, err)
	}

	return ops
}

// waitForTwoRuns polls until at least two operations of kind reached a
// terminal state, or fails after scheduleTestDeadline. Ticks collapse when
// no instance runs them, so the count is load dependent: two ended runs
// prove progress without a fixed window. Ended runs also prove the job body
// ran, not only that the tick was claimed.
func waitForTwoRuns(t *testing.T, srv *Server, kind durable.Kind) {
	t.Helper()

	waitForRuns(t, srv, kind, 2)
}

// waitForRuns polls until at least want operations of kind reached a terminal
// state, or fails after scheduleTestDeadline.
func waitForRuns(t *testing.T, srv *Server, kind durable.Kind, want int) {
	t.Helper()

	deadline := time.Now().Add(scheduleTestDeadline)

	for {
		ops := listOps(t, srv, kind)
		ended := 0

		for i := range ops {
			if ops[i].State.Terminal() {
				ended++
			}
		}

		if ended >= want {
			return
		}

		if time.Now().After(deadline) {
			t.Fatalf("ended operations of %s = %d, want at least %d", kind, ended, want)
		}

		select {
		case <-t.Context().Done():
			t.Fatalf("test context done while waiting for two runs of %s: %v", kind, t.Context().Err())
		case <-time.After(20 * time.Millisecond):
		}
	}
}

// checkTickKeysOnce fails when two operations of kind share one tick key.
// Each operation key is the tick key of its tick, so a duplicate key means
// one tick ran twice. Unique keys bound the runs above without a fixed
// window.
func checkTickKeysOnce(t *testing.T, srv *Server, kind durable.Kind) {
	t.Helper()

	ops := listOps(t, srv, kind)

	if len(ops) < 2 {
		t.Fatalf("operations of %s = %d, want at least 2: two runs prove progress", kind, len(ops))
	}

	seen := make(map[string]struct{}, len(ops))

	for i := range ops {
		key := ops[i].Key
		if key == "" {
			t.Fatalf("operation of %s has an empty key: each run records its tick key", kind)
		}

		if _, dup := seen[key]; dup {
			t.Fatalf("tick key %q of %s ran twice: one instance runs each tick", key, kind)
		}

		seen[key] = struct{}{}
	}
}

// testServer returns a Server with only the fields that the job tests use.
func testServer(durableStore DurableStore, owner string) *Server {
	return &Server{
		kind:             kindState{store: nil, principals: nil, kind: ""},
		instances:        instanceState{store: nil, id: "", version: "", registered: nil},
		ready:            nil,
		drainPeriod:      0,
		tempAdmins:       nil,
		manualLocks:      nil,
		reconciler:       nil,
		artifactSweeper:  nil,
		nameLocks:        nameLockPrune{locks: nil},
		issuer:           nil,
		handler:          nil,
		started:          nil,
		ungated:          nil,
		mounted:          nil,
		root:             nil,
		logger:           slog.New(slog.DiscardHandler),
		listenAddr:       "",
		listener:         nil,
		secrets:          nil,
		secretKeys:       nil,
		secretWrapper:    nil,
		workspaces:       nil,
		backups:          nil,
		restores:         nil,
		storeChecks:      nil,
		now:              nil,
		registry:         nil,
		tlsCertFile:      "",
		tlsKeyFile:       "",
		observed:         nil,
		chain:            nil,
		tokens:           nil,
		shipper:          nil,
		beforeServe:      nil,
		deleted:          nil,
		signInRequests:   nil,
		signInPruneEvery: 0,
		signInProviders:  providerBinding{store: nil, providers: nil},
		durable:          durableStore,
		durableOwner:     owner,
		inactivityEvery:  scheduleTestEvery,
		deletedEvery:     scheduleTestEvery,
		idempotency:      nil,
		idempotencyEvery: 0,
		auditChainEvery:  0,
		components:       AllComponents(),
		watch:            nil,
		flushDenials:     nil,
		stateWrite: stateWriteStart{
			effective: stategate.Effective{Mode: "", Reason: "", Checked: nil, Unchecked: nil},
			audit:     nil,
		},
	}
}

// waitForPurge polls until every unit of units has no blob left, or fails
// after 10 seconds. The sweep purges every due unit in each run, so two
// ended runs prove progress but not that an in-flight run finished its
// page: teardown must wait for the blobs, not only for the runs.
func waitForPurge(t *testing.T, f sweepFixture, units []sweptUnit) {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)

	for {
		pendingID := ""
		pendingN := 0

		for _, u := range units {
			if n := f.blobCount(t, u.prefix); n != 0 {
				pendingID, pendingN = u.id, n

				break
			}
		}

		if pendingID == "" {
			return
		}

		if time.Now().After(deadline) {
			t.Fatalf("blobs of %s = %d, want 0 after 10 seconds: the sweep purged the unit", pendingID, pendingN)
		}

		select {
		case <-t.Context().Done():
			t.Fatalf("test context done while waiting for the purge of %s: %v", pendingID, t.Context().Err())
		case <-time.After(20 * time.Millisecond):
		}
	}
}

// TestJobSchedulesRunDeletedSweepOncePerTick proves that two instances on one
// store purge each deleted unit once per tick: the store holds about one
// operation per tick, not two, and the units are gone. The intervals are
// short for the test; the production intervals stay one hour.
func TestJobSchedulesRunDeletedSweepOncePerTick(t *testing.T) {
	t.Parallel()

	f := newSweepFixture(t)
	units := f.deleteUnits(t)
	f.clock.advance(2 * time.Hour)

	a := testServer(f.store.DurableOperations(), "a")
	a.deleted, a.observed = f.sweeper, f.observed
	a.deletedEvery = scheduleTestEvery

	b := testServer(f.store.DurableOperations(), "b")
	b.deleted, b.observed = f.sweeper, f.observed
	b.deletedEvery = scheduleTestEvery

	aPair, bPair := newPair(t, a), newPair(t, b)

	ctx, cancel := context.WithTimeout(t.Context(), scheduleTestDeadline)
	defer cancel()

	var wg sync.WaitGroup

	for _, r := range []func(context.Context){aPair.Sched.Run, aPair.Run.Run, bPair.Sched.Run, bPair.Run.Run} {
		wg.Go(func() { r(ctx) })
	}

	waitForTwoRuns(t, a, kindDeleted)
	waitForPurge(t, f, units)
	cancel()
	wg.Wait()

	checkTickKeysOnce(t, a, kindDeleted)
}

// TestJobSchedulesRunSignInPruneOncePerTick proves that two instances on one
// store prune the expired sign-in requests once per tick. The intervals are
// short for the test; the production interval stays one request lifetime.
func TestJobSchedulesRunSignInPruneOncePerTick(t *testing.T) {
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

	sessions, err := session.NewSessions(session.SessionsConfig{
		Store: st, Providers: nil, Inactivity: nil, Now: time.Now,
	})
	if err != nil {
		t.Fatalf("sessions: %v", err)
	}

	for range 2 {
		state, stateErr := session.NewSecret()
		if stateErr != nil {
			t.Fatalf("state: %v", stateErr)
		}

		begin := session.Begin{
			State: state, Provider: "corp", Nonce: "n-0S6_WzA2Mj-rG7k3QpL",
			Verifier: "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk", ReturnPath: "/", MaxAge: 0, HasMaxAge: false,
		}
		if beginErr := sessions.BeginRequest(t.Context(), begin); beginErr != nil {
			t.Fatalf("begin: %v", beginErr)
		}
	}

	a := testServer(st.DurableOperations(), "a")
	a.signInRequests, a.signInPruneEvery = sessions, scheduleTestEvery

	b := testServer(st.DurableOperations(), "b")
	b.signInRequests, b.signInPruneEvery = sessions, scheduleTestEvery

	aPair, bPair := newPair(t, a), newPair(t, b)

	ctx, cancel := context.WithTimeout(t.Context(), scheduleTestDeadline)
	defer cancel()

	var wg sync.WaitGroup

	for _, r := range []func(context.Context){aPair.Sched.Run, aPair.Run.Run, bPair.Sched.Run, bPair.Run.Run} {
		wg.Go(func() { r(ctx) })
	}

	waitForTwoRuns(t, a, kindSignInPrune)
	cancel()
	wg.Wait()

	checkTickKeysOnce(t, a, kindSignInPrune)
}

// TestJobSchedulesRunInactivityDisableOncePerTick proves that two instances on
// one store run the inactivity disable once per tick. The intervals are short
// for the test; the production interval stays one hour.
func TestJobSchedulesRunInactivityDisableOncePerTick(t *testing.T) {
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

	tokens, err := accesstoken.NewService(accesstoken.Config{
		TempAdmins: nil,
		Store:      st, Principals: st, Freshness: nil, Authorizer: nil,
		Reach: access.ReachConfig{
			Grants: nil, Roles: nil, Callers: nil, Principals: nil, Collections: nil, Units: nil,
		},
		Logger: slog.New(slog.DiscardHandler), Now: time.Now, NewID: nil,
	})
	if err != nil {
		t.Fatalf("token service: %v", err)
	}

	observed := metrics.NewServerMetrics("test", time.Now())

	a := testServer(st.DurableOperations(), "a")
	a.tokens, a.observed = tokens, observed
	a.inactivityEvery = scheduleTestEvery

	b := testServer(st.DurableOperations(), "b")
	b.tokens, b.observed = tokens, observed
	b.inactivityEvery = scheduleTestEvery

	aPair, bPair := newPair(t, a), newPair(t, b)

	ctx, cancel := context.WithTimeout(t.Context(), scheduleTestDeadline)
	defer cancel()

	var wg sync.WaitGroup

	for _, r := range []func(context.Context){aPair.Sched.Run, aPair.Run.Run, bPair.Sched.Run, bPair.Run.Run} {
		wg.Go(func() { r(ctx) })
	}

	waitForTwoRuns(t, a, kindInactivity)
	cancel()
	wg.Wait()

	checkTickKeysOnce(t, a, kindInactivity)
}

// TestJobScheduleReconcileRuns proves that a server with a reconciler lists the
// schedule job.reconcile with an interval of one minute and a runner kind, and
// that the first tick runs the reconcile to a successful end.
func TestJobScheduleReconcileRuns(t *testing.T) {
	t.Parallel()

	store, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := store.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	reconciler, err := job.NewReconciler(job.ReconcilerConfig{
		Store: store.Jobs(), Namespaces: store, QueueTimeout: 0, DispatchTimeout: 0, StateTimeout: 0,
		MaxPlanAge: 0, Approvers: gate.ApproverPolicy{Production: true, IsDev: nil}, Batch: 0, Now: nil, Log: nil,
	})
	if err != nil {
		t.Fatalf("new reconciler: %v", err)
	}

	srv := testServer(store.DurableOperations(), "a")
	srv.reconciler = reconciler

	var found *durable.ScheduleSpec

	for _, spec := range srv.jobSchedules() {
		if spec.Name == job.ReconcileName {
			found = &spec
		}
	}

	if found == nil || found.Kind != job.KindReconcile || found.Interval != time.Minute {
		t.Fatalf("schedules = %+v, want %s every minute with kind %s", srv.jobSchedules(), job.ReconcileName,
			job.KindReconcile)
	}

	pair := newPair(t, srv)

	ctx, cancel := context.WithTimeout(t.Context(), scheduleTestDeadline)
	defer cancel()

	var wg sync.WaitGroup

	wg.Go(func() { pair.Sched.Run(ctx) })
	wg.Go(func() { pair.Run.Run(ctx) })

	waitForRuns(t, srv, job.KindReconcile, 1)
	cancel()
	wg.Wait()

	for _, op := range listOps(t, srv, job.KindReconcile) {
		if op.State != durable.StateSucceeded {
			t.Fatalf("operation %s of the reconcile = %s, want succeeded", op.ID, op.State)
		}
	}
}

// newArtifactSweepServer returns a test server with the artifact sweep over a
// fresh SQLite store and an empty blob store.
func newArtifactSweepServer(t *testing.T) *Server {
	t.Helper()

	store, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := store.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	blobs, err := blobfs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open blob store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := blobs.Close(); closeErr != nil {
			t.Errorf("close blob store: %v", closeErr)
		}
	})

	artifacts, err := job.NewArtifactBlobs(blobs, dropRefusals{})
	if err != nil {
		t.Fatalf("artifact blobs: %v", err)
	}

	sweeper, err := job.NewArtifactSweeper(job.ArtifactSweeperConfig{
		Jobs: store.Jobs(), Names: store.Jobs(), Blobs: artifacts, Namespaces: store, Guard: store,
		Clock: store, Operations: store.DurableOperations(), Audit: store, Logger: nil, Now: nil,
	})
	if err != nil {
		t.Fatalf("new sweeper: %v", err)
	}

	srv := testServer(store.DurableOperations(), "a")
	srv.artifactSweeper = sweeper

	return srv
}

// runArtifactSweepStep runs the step of the kind of the artifact sweep of srv,
// and checks that its output is the result of a run that swept.
func runArtifactSweepStep(t *testing.T, srv *Server) {
	t.Helper()

	var kind *durable.KindSpec

	for _, k := range srv.jobKinds() {
		if k.Kind == job.KindArtifactSweep {
			kind = &k
		}
	}

	if kind == nil || len(kind.Steps) != 1 || kind.Version != job.ArtifactSweepVersion {
		t.Fatalf("kinds = %+v, want %s with one step", srv.jobKinds(), job.KindArtifactSweep)
	}

	// The step reads nothing of its context.
	var step durable.StepContext

	out, err := kind.Steps[0].Run(t.Context(), step)
	if err != nil {
		t.Fatalf("run the step of the artifact sweep: %v", err)
	}

	var res job.ArtifactSweepResult
	if decodeErr := json.Unmarshal(out, &res); decodeErr != nil || res.Skipped != "" || res.DeletedKeys == nil {
		t.Fatalf("step output = %s; want the result of a run that swept", out)
	}
}

// TestJobScheduleArtifactSweepRuns proves that a server with the artifact
// sweep lists the schedule job.artifact-sweep with an interval of one hour and
// a runner kind whose step runs the sweep to a successful end.
func TestJobScheduleArtifactSweepRuns(t *testing.T) {
	t.Parallel()

	srv := newArtifactSweepServer(t)

	var found *durable.ScheduleSpec

	for _, spec := range srv.jobSchedules() {
		if spec.Name == job.ArtifactSweepName {
			found = &spec
		}
	}

	if found == nil || found.Kind != job.KindArtifactSweep || found.Interval != time.Hour {
		t.Fatalf("schedules = %+v, want %s every hour with kind %s", srv.jobSchedules(), job.ArtifactSweepName,
			job.KindArtifactSweep)
	}

	// The interval is one hour, so the case runs the step of the kind itself
	// instead of waiting for a tick.
	runArtifactSweepStep(t, srv)
}

// dropRefusals is a scopeaudit.Recorder that drops every refusal.
type dropRefusals struct{}

func (dropRefusals) RecordOutOfScope(context.Context, namespace.ID, string, scopeaudit.Where) error {
	return nil
}

// TestJobScheduleNameLockPruneRuns proves that a server whose store keeps
// the counts of the lockout by name lists the schedule name-lock-prune with
// the interval that the API shows, and a runner kind whose step runs one
// prune: its output is the result, and the store holds the entry of the run.
func TestJobScheduleNameLockPruneRuns(t *testing.T) {
	t.Parallel()

	store, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := store.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	srv := testServer(store.DurableOperations(), "a")
	srv.nameLocks = nameLockPrune{locks: store}
	srv.now = time.Now

	var found *durable.ScheduleSpec

	for _, spec := range srv.jobSchedules() {
		if spec.Name == nameLockPruneScheduleName {
			found = &spec
		}
	}

	if found == nil || found.Kind != kindNameLockPrune || found.Interval != localusersvc.Prune().Interval {
		t.Fatalf("schedules = %+v, want %s every %s", srv.jobSchedules(), nameLockPruneScheduleName,
			localusersvc.Prune().Interval)
	}

	runNameLockPruneStep(t, srv)

	if runs := pruneEntries(t, store); runs != 1 {
		t.Fatalf("entries of the prune = %d, want 1", runs)
	}
}

// runNameLockPruneStep runs the step of the kind of the prune of srv, and
// checks that its output is the result of a run.
func runNameLockPruneStep(t *testing.T, srv *Server) {
	t.Helper()

	var kind *durable.KindSpec

	for _, k := range srv.jobKinds() {
		if k.Kind == kindNameLockPrune {
			kind = &k
		}
	}

	if kind == nil || len(kind.Steps) != 1 {
		t.Fatalf("kinds = %+v, want %s with one step", srv.jobKinds(), kindNameLockPrune)
	}

	var step durable.StepContext

	out, err := kind.Steps[0].Run(t.Context(), step)
	if err != nil {
		t.Fatalf("run the step of the prune: %v", err)
	}

	var res localusersvc.PruneResult
	if decodeErr := json.Unmarshal(out, &res); decodeErr != nil || res.MaxRows != localusersvc.Prune().MaxRows {
		t.Fatalf("step output = %s; want the result of a run", out)
	}
}

// pruneEntries returns the count of the audit entries of the prune in store.
func pruneEntries(t *testing.T, store *sqlite.Store) int {
	t.Helper()

	page, err := store.List(t.Context(), audit.ServerScope, audit.Page{Number: 1, Size: 50})
	if err != nil {
		t.Fatalf("list entries: %v", err)
	}

	runs := 0

	for i := range page.Entries {
		if page.Entries[i].Action == localusersvc.ActionNameLocksPruned {
			runs++
		}
	}

	return runs
}

// TestNoNameLockPruneWithoutCounts proves that a server whose store keeps no
// counts of the lockout by name lists no prune schedule and no prune kind.
func TestNoNameLockPruneWithoutCounts(t *testing.T) {
	t.Parallel()

	srv := testServer(nil, "a")

	for _, spec := range srv.jobSchedules() {
		if spec.Name == nameLockPruneScheduleName {
			t.Fatalf("schedules = %+v, want no %s", srv.jobSchedules(), nameLockPruneScheduleName)
		}
	}

	for _, k := range srv.jobKinds() {
		if k.Kind == kindNameLockPrune {
			t.Fatalf("kinds = %+v, want no %s", srv.jobKinds(), kindNameLockPrune)
		}
	}
}

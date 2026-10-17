package dustpan

import (
	"context"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/metrics"
	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/session"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// scheduleTestEvery is the tick of the schedule tests: short enough for a
// fast test, long enough for the store clock to tell ticks apart.
const scheduleTestEvery = 50 * time.Millisecond

// scheduleTestRun is how long each schedule test runs its two instances.
const scheduleTestRun = 600 * time.Millisecond

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

// countOps returns how many operations of kind the store holds.
func countOps(t *testing.T, srv *Server, kind durable.Kind) int {
	t.Helper()

	ops, err := srv.durable.List(t.Context(), durable.Filter{Kind: kind, States: nil, Limit: 0})
	if err != nil {
		t.Fatalf("list operations of %s: %v", kind, err)
	}

	return len(ops)
}

// checkOneRunPerTick fails unless kind ran about once per tick: at least four
// times, and at most one more than the ticks in scheduleTestRun. Two
// instances that each ran each tick would hold about twice as many.
func checkOneRunPerTick(t *testing.T, srv *Server, kind durable.Kind) {
	t.Helper()

	got := countOps(t, srv, kind)
	most := int(scheduleTestRun/scheduleTestEvery) + 2

	if got < 4 || got > most {
		t.Fatalf("operations of %s = %d, want 4 to %d: one per tick", kind, got, most)
	}
}

// testServer returns a Server with only the fields that the job tests use.
func testServer(durableStore DurableStore, owner string) *Server {
	return &Server{
		logger:           slog.New(slog.DiscardHandler),
		observed:         nil,
		chain:            nil,
		tokens:           nil,
		deleted:          nil,
		signInRequests:   nil,
		signInPruneEvery: 0,
		durable:          durableStore,
		durableOwner:     owner,
		inactivityEvery:  scheduleTestEvery,
		deletedEvery:     scheduleTestEvery,
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

	ctx, cancel := context.WithTimeout(t.Context(), scheduleTestRun)
	defer cancel()

	var wg sync.WaitGroup

	for _, r := range []func(context.Context){aPair.Sched.Run, aPair.Run.Run, bPair.Sched.Run, bPair.Run.Run} {
		wg.Go(func() { r(ctx) })
	}

	<-ctx.Done()
	wg.Wait()

	checkOneRunPerTick(t, a, kindDeleted)

	for _, u := range units {
		if n := f.blobCount(t, u.prefix); n != 0 {
			t.Fatalf("blobs of %s = %d, want 0: the sweep purged the unit", u.id, n)
		}
	}
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

	ctx, cancel := context.WithTimeout(t.Context(), scheduleTestRun)
	defer cancel()

	var wg sync.WaitGroup

	for _, r := range []func(context.Context){aPair.Sched.Run, aPair.Run.Run, bPair.Sched.Run, bPair.Run.Run} {
		wg.Go(func() { r(ctx) })
	}

	<-ctx.Done()
	wg.Wait()

	checkOneRunPerTick(t, a, kindSignInPrune)
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
		Store: st, Principals: st, Freshness: nil, Authorizer: nil,
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

	ctx, cancel := context.WithTimeout(t.Context(), scheduleTestRun)
	defer cancel()

	var wg sync.WaitGroup

	for _, r := range []func(context.Context){aPair.Sched.Run, aPair.Run.Run, bPair.Sched.Run, bPair.Run.Run} {
		wg.Go(func() { r(ctx) })
	}

	<-ctx.Done()
	wg.Wait()

	checkOneRunPerTick(t, a, kindInactivity)
}

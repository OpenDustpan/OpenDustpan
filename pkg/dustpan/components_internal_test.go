package dustpan

import (
	"context"
	"log/slog"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// openTestStore opens a SQLite store that the test closes.
func openTestStore(t *testing.T) *sqlite.Store {
	t.Helper()

	st, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := st.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	return st
}

// apiOnly, workerOnly are the sets of one component.
func apiOnly() Components    { return Components{api: true, worker: false} }
func workerOnly() Components { return Components{api: false, worker: true} }

// newTestWatch returns a watch over store with the given clock.
func newTestWatch(store DurableStore, set Components, lease time.Duration, now func() time.Time) *workerWatch {
	return &workerWatch{
		store:       store,
		specs:       func() []durable.ScheduleSpec { return nil },
		lease:       lease,
		pollEvery:   time.Second,
		components:  set,
		owner:       "test-" + time.Now().Format("150405.000000"),
		logger:      slog.New(slog.DiscardHandler),
		now:         now,
		lastActive:  atomic.Int64{},
		lastBeat:    atomic.Int64{},
		mu:          sync.Mutex{},
		cached:      emptyReport(),
		cachedAt:    time.Time{},
		cachedOK:    false,
		watchedFrom: time.Time{},
	}
}

// tickCase is one case of TestStartBackgroundByComponent.
type tickCase struct {
	name      string
	set       Components
	wantTicks bool
}

// TestStartBackgroundByComponent proves that only the worker component runs
// the job schedules. With the worker, the tick of a short schedule starts an
// operation. With only the api, the schedule exists and nobody claims it.
func TestStartBackgroundByComponent(t *testing.T) {
	t.Parallel()

	cases := []tickCase{
		{name: "worker runs the schedules", set: workerOnly(), wantTicks: true},
		{name: "all run the schedules", set: AllComponents(), wantTicks: true},
		{name: "api runs none", set: apiOnly(), wantTicks: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			st := openTestStore(t)
			srv := testServer(st.DurableOperations(), "a")

			tokens, err := accesstoken.NewService(accesstoken.Config{
				TempAdmins: nil, Store: st, Principals: st, Freshness: nil, Authorizer: nil,
				Reach: access.ReachConfig{
					Grants: nil, Roles: nil, Callers: nil, Principals: nil, Collections: nil, Units: nil,
				},
				Logger: srv.logger, Now: time.Now, NewID: nil,
			})
			if err != nil {
				t.Fatalf("token service: %v", err)
			}

			srv.tokens, srv.components = tokens, tc.set
			srv.watch = newTestWatch(st.DurableOperations(), tc.set, 0, time.Now)
			srv.watch.specs = srv.jobSchedules

			ctx, cancel := context.WithTimeout(t.Context(), 1500*time.Millisecond)
			defer cancel()

			var sweeps sync.WaitGroup

			if startErr := srv.startBackground(ctx, &sweeps); startErr != nil {
				t.Fatalf("start background: %v", startErr)
			}

			sweeps.Wait()

			if got := ticked(t, srv, st); got != tc.wantTicks {
				t.Fatalf("schedule ticked = %v, want %v", got, tc.wantTicks)
			}
		})
	}
}

// ticked reports whether the inactivity schedule has run a tick: it started
// an operation and the schedule records a claim. The two signs must agree.
func ticked(t *testing.T, srv *Server, st *sqlite.Store) bool {
	t.Helper()

	started := len(listOps(t, srv, kindInactivity)) > 0

	sched, err := st.DurableOperations().GetSchedule(t.Context(), inactivityScheduleName)
	if err != nil {
		t.Fatalf("schedule %s: %v", inactivityScheduleName, err)
	}

	if claimed := !sched.LastClaimedAt.IsZero(); claimed != started {
		t.Fatalf("schedule claimed = %v but operation started = %v", claimed, started)
	}

	return started
}

// sinceCase is one case of TestUnclaimedSince.
type sinceCase struct {
	name string
	op   durable.Operation
	want time.Time
	ok   bool
}

// operationAt returns an operation with the fields that the rule reads.
func operationAt(
	state durable.State, created, notBefore, expires time.Time,
) durable.Operation {
	return durable.Operation{
		ID: "", Key: "", Kind: "", Version: 0, Input: nil, State: state, Steps: nil, Attempt: 0,
		Failures: 0, Expiries: 0, LastError: "", CancelRequested: false, UpdatedAt: time.Time{},
		Claim:     durable.Claim{Operation: "", Owner: "", Token: 0, ExpiresAt: expires},
		NotBefore: notBefore, CreatedAt: created,
	}
}

// TestUnclaimedSince proves when an operation counts as unclaimed.
func TestUnclaimedSince(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.October, 19, 12, 0, 0, 0, time.UTC)
	ago := func(d time.Duration) time.Time { return now.Add(-d) }
	none := time.Time{}

	cases := []sinceCase{
		{
			name: "pending since creation",
			op:   operationAt(durable.StatePending, ago(time.Minute), none, none),
			want: ago(time.Minute), ok: true,
		},
		{
			name: "pending after a retry waits from not-before",
			op:   operationAt(durable.StatePending, ago(time.Hour), ago(10*time.Second), none),
			want: ago(10 * time.Second), ok: true,
		},
		{
			name: "pending with not-before in the future is not due",
			op:   operationAt(durable.StatePending, ago(time.Hour), now.Add(time.Minute), none),
			want: none, ok: false,
		},
		{
			name: "running with a live claim",
			op:   operationAt(durable.StateRunning, ago(time.Hour), none, now.Add(time.Second)),
			want: none, ok: false,
		},
		{
			name: "running with an expired claim",
			op:   operationAt(durable.StateRunning, ago(time.Hour), none, ago(time.Minute)),
			want: ago(time.Minute), ok: true,
		},
		{
			name: "ended",
			op:   operationAt(durable.StateSucceeded, ago(time.Hour), none, none),
			want: none, ok: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, ok := unclaimedSince(&tc.op, now)
			if ok != tc.ok || (ok && !got.Equal(tc.want)) {
				t.Fatalf("unclaimedSince = %v, %v; want %v, %v", got, ok, tc.want, tc.ok)
			}
		})
	}
}

// TestClaimLoopRan proves the worker readiness rule: the claim loop counts as
// running when it asked the store within 2 claim intervals.
func TestClaimLoopRan(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.October, 19, 12, 0, 0, 0, time.UTC)
	clock := start
	w := newTestWatch(nil, workerOnly(), 10*time.Second, func() time.Time { return clock })

	if !w.claimLoopRan() {
		t.Fatal("a watch with no store must read as running")
	}

	w.store = noStore{DurableStore: nil}
	w.touch()

	for _, step := range []struct {
		after time.Duration
		want  bool
	}{
		{after: 19 * time.Second, want: true},
		{after: 21 * time.Second, want: false},
	} {
		clock = start.Add(step.after)

		if got := w.claimLoopRan(); got != step.want {
			t.Fatalf("claim loop ran = %v %s after a claim, want %v", got, step.after, step.want)
		}
	}

	w.touch()

	if !w.claimLoopRan() {
		t.Fatal("claim loop must read as running after a new claim")
	}
}

// noStore is a DurableStore that the tests never call.
type noStore struct{ DurableStore }

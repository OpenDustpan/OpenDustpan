package dustpan

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	noworker "opendustpan.org/opendustpan/internal/api/admit"
	"opendustpan.org/opendustpan/pkg/durable"
)

// beatLease is the lease period of the heartbeat tests. Two periods are
// 400 ms, and a heartbeat goes out at most every 50 ms.
const beatLease = 200 * time.Millisecond

// pauseFor waits for d or until the test context ends.
func pauseFor(t *testing.T, d time.Duration) {
	t.Helper()

	select {
	case <-time.After(d):
	case <-t.Context().Done():
	}
}

// heldLease is the lease of a claim that a test holds while it waits in real
// time. It is far longer than any stall of a loaded machine, so the claim
// never ends before the test lets it end. The heartbeat has its own period,
// beatLease.
const heldLease = time.Hour

// beatWait is a wait that is past 2 lease periods.
const beatWait = 2*beatLease + 250*time.Millisecond

// workerOf returns the watch of a worker process over the store of api, as a
// second process would hold it.
func workerOf(api *workerWatch) (*workerWatch, trackingStore) {
	worker := newTestWatch(api.store, workerOnly(), api.lease, time.Now)
	worker.owner = "worker-1"

	return worker, trackingStore{DurableStore: api.store, watch: worker}
}

// claimAttempt makes one claim attempt through the tracking store of a
// worker. No operation is due, so the claim finds no work: the attempt
// still counts as a heartbeat.
func claimAttempt(t *testing.T, tracked trackingStore) {
	t.Helper()

	_, err := tracked.Claim(t.Context(), durable.ClaimRequest{
		Owner: "worker-1", Kinds: []durable.Kind{"none"}, Lease: beatLease, MaxVersions: nil,
		TakeCanceled: false,
	})
	if !errors.Is(err, durable.ErrNoWork) {
		t.Fatalf("claim = %v, want ErrNoWork", err)
	}
}

// startOldOperation starts an operation that no worker claims.
func startOldOperation(t *testing.T, store durable.Store) {
	t.Helper()

	_, _, err := store.Start(t.Context(), durable.StartRequest{Key: "k", Kind: "any", Version: 0, Input: nil})
	if err != nil {
		t.Fatalf("start operation: %v", err)
	}
}

// admit answers the result of the start gate of w, with a fresh read.
func admitStartOf(t *testing.T, w *workerWatch) error {
	t.Helper()

	w.mu.Lock()
	w.cachedOK = false
	w.mu.Unlock()

	return w.AdmitStart(t.Context())
}

// TestNoWorkerRefusesAfterTwoLeasesAndNotBefore proves that with no worker
// the start gate admits until 2 lease periods passed since the process began
// to watch, and refuses after, also when the store holds an old operation.
func TestNoWorkerRefusesAfterTwoLeasesAndNotBefore(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	startOldOperation(t, st.DurableOperations())

	api := newTestWatch(st.DurableOperations(), apiOnly(), beatLease, time.Now)
	api.begin(t.Context())

	if err := admitStartOf(t, api); err != nil {
		t.Fatalf("start right after the process began: %v, want it admitted", err)
	}

	pauseFor(t, beatWait)

	err := admitStartOf(t, api)
	if !errors.Is(err, noworker.ErrNoWorker) {
		t.Fatalf("start after 2 leases with no worker: %v, want ErrNoWorker", err)
	}
}

// TestBusyWorkerGivesNoRefusal proves that a worker that holds one long job
// and only renews its claim keeps the gate open, while other work waits
// unclaimed longer than 2 lease periods. The claim has a long lease: a stall
// of the test on a loaded machine must not end the claim, because the case
// tests the heartbeat and not the end of a claim.
func TestBusyWorkerGivesNoRefusal(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	store := st.DurableOperations()
	startOldOperation(t, store)

	api := newTestWatch(store, apiOnly(), beatLease, time.Now)
	api.begin(t.Context())

	_, tracked := workerOf(api)

	claim, err := tracked.Claim(t.Context(), durable.ClaimRequest{
		Owner: "worker-1", Kinds: []durable.Kind{"any"}, Lease: heldLease, MaxVersions: map[durable.Kind]int{"any": 0},
		TakeCanceled: false,
	})
	if err != nil {
		t.Fatalf("claim: %v", err)
	}

	startOldOperation2(t, store)

	held := claim.Claim
	deadline := time.Now().Add(beatWait + beatLease)

	for time.Now().Before(deadline) {
		pauseFor(t, beatLease/8)

		held, err = tracked.Extend(t.Context(), held, heldLease)
		if err != nil {
			t.Fatalf("extend: %v", err)
		}
	}

	if admitErr := admitStartOf(t, api); admitErr != nil {
		t.Fatalf("start while a worker renews a claim: %v, want it admitted", admitErr)
	}

	report, err := api.WorkerReport(t.Context())
	if err != nil {
		t.Fatalf("report: %v", err)
	}

	if report.Stuck || report.OldestUnclaimedSeconds <= 2*beatLease.Seconds() {
		t.Fatalf("report = %+v, want a live worker and an operation that waits past 2 leases", report)
	}
}

// startOldOperation2 starts a second operation, so that one waits while the
// other runs.
func startOldOperation2(t *testing.T, store durable.Store) {
	t.Helper()

	_, _, err := store.Start(t.Context(), durable.StartRequest{Key: "k2", Kind: "other", Version: 0, Input: nil})
	if err != nil {
		t.Fatalf("start operation: %v", err)
	}
}

// clockedBeats is the durable store of a test with the heartbeats on a clock
// that the test moves. Beat and LastBeat keep the beats in memory at the time
// of clock; every other call goes to the wrapped store. So a test of the
// heartbeat rule waits for no wall-clock time and reads the same times on any
// machine.
type clockedBeats struct {
	DurableStore

	clock *testClock
	mu    *sync.Mutex
	beats map[string]durable.Beat
}

// newClockedBeats returns store with its heartbeats on clock.
func newClockedBeats(store DurableStore, clock *testClock) clockedBeats {
	return clockedBeats{DurableStore: store, clock: clock, mu: &sync.Mutex{}, beats: map[string]durable.Beat{}}
}

// Beat records the beat of owner at the time of the clock.
func (c clockedBeats) Beat(_ context.Context, owner string, lease time.Duration) error {
	if owner == "" || lease <= 0 {
		return durable.ErrInvalid
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.beats[owner] = durable.Beat{Owner: owner, At: c.clock.Now(), Lease: lease, Now: time.Time{}}

	return nil
}

// LastBeat returns the beat whose time plus 2 lease periods is the latest, as
// the port says, with Now from the clock.
func (c clockedBeats) LastBeat(context.Context) (durable.Beat, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	last := durable.Beat{Owner: "", At: time.Time{}, Lease: 0, Now: c.clock.Now()}

	for _, b := range c.beats {
		end, lastEnd := b.At.Add(2*b.Lease), last.At.Add(2*last.Lease)
		if last.Owner == "" || end.After(lastEnd) || (end.Equal(lastEnd) && b.At.After(last.At)) ||
			(end.Equal(lastEnd) && b.At.Equal(last.At) && b.Owner > last.Owner) {
			last = durable.Beat{Owner: b.Owner, At: b.At, Lease: b.Lease, Now: last.Now}
		}
	}

	return last, nil
}

// clockedWatches returns an api watch and the tracking store of a worker
// watch over store, both on clock, with the heartbeats of store on clock.
func clockedWatches(store DurableStore, clock *testClock) (*workerWatch, trackingStore) {
	beats := newClockedBeats(store, clock)

	api := newTestWatch(beats, apiOnly(), beatLease, clock.Now)
	worker := newTestWatch(beats, workerOnly(), beatLease, clock.Now)
	worker.owner = "worker-1"

	return api, trackingStore{DurableStore: beats, watch: worker}
}

// TestRollingUpgradeGivesNoRefusal proves that a due schedule that no worker
// knows, as during a rolling upgrade while only older workers run, shows in
// the report but does not make the gate refuse while the workers beat. The
// heartbeats run on a clock that the test moves: the worker tries a claim each
// eighth of a lease period, for more than 3 lease periods, with no wait.
func TestRollingUpgradeGivesNoRefusal(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	store := st.DurableOperations()

	spec := durable.ScheduleSpec{
		Name: "new-job", Kind: "new-job", Version: 1, Interval: time.Hour, Input: nil,
	}
	if _, err := store.EnsureSchedule(t.Context(), spec); err != nil {
		t.Fatalf("ensure schedule: %v", err)
	}

	clock := &testClock{mu: sync.Mutex{}, now: time.Unix(1_800_000_000, 0).UTC()}
	api, tracked := clockedWatches(store, clock)
	api.specs = func() []durable.ScheduleSpec { return []durable.ScheduleSpec{spec} }
	api.begin(t.Context())

	end := clock.Now().Add(beatWait + beatLease)

	for clock.Now().Before(end) {
		claimAttempt(t, tracked)

		if err := admitStartOf(t, api); err != nil {
			t.Fatalf("start at %s while old workers beat: %v, want it admitted", clock.Now(), err)
		}

		clock.Set(clock.Now().Add(beatLease / 8))
	}

	report, err := api.WorkerReport(t.Context())
	if err != nil {
		t.Fatalf("report: %v", err)
	}

	if report.Stuck || len(report.Schedules) != 1 || report.Schedules[0].Name != "new-job" {
		t.Fatalf("report = %+v, want a live worker and the due schedule new-job", report)
	}
}

// TestSilenceOfExactlyTwoLeasesIsAdmitted pins the boundary of the rule "last
// beat plus 2 lease periods" on the clock of the test: a silence of exactly 2
// lease periods is admitted, and 1 ms more is refused.
func TestSilenceOfExactlyTwoLeasesIsAdmitted(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		silence time.Duration
		want    error
	}{
		{name: "no silence", silence: 0, want: nil},
		{name: "1 ms under 2 leases", silence: 2*beatLease - time.Millisecond, want: nil},
		{name: "exactly 2 leases", silence: 2 * beatLease, want: nil},
		{name: "1 ms over 2 leases", silence: 2*beatLease + time.Millisecond, want: noworker.ErrNoWorker},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			st := openTestStore(t)
			clock := &testClock{mu: sync.Mutex{}, now: time.Unix(1_800_000_000, 0).UTC()}

			api, tracked := clockedWatches(st.DurableOperations(), clock)
			api.begin(t.Context())

			claimAttempt(t, tracked)
			clock.Set(clock.Now().Add(tc.silence))

			err := admitStartOf(t, api)
			if !errors.Is(err, tc.want) || (tc.want == nil && err != nil) {
				t.Fatalf("start after a silence of %s: %v, want %v", tc.silence, err, tc.want)
			}
		})
	}
}

// TestWorkerThatStopsIsMissedAfterTwoLeases proves that the gate refuses once
// the last heartbeat is older than 2 lease periods.
func TestWorkerThatStopsIsMissedAfterTwoLeases(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	api := newTestWatch(st.DurableOperations(), apiOnly(), beatLease, time.Now)
	api.begin(t.Context())

	_, tracked := workerOf(api)

	claimAttempt(t, tracked)

	if err := admitStartOf(t, api); err != nil {
		t.Fatalf("start right after a heartbeat: %v, want it admitted", err)
	}

	pauseFor(t, beatWait)

	if err := admitStartOf(t, api); !errors.Is(err, noworker.ErrNoWorker) {
		t.Fatalf("start 2 leases after the last heartbeat: %v, want ErrNoWorker", err)
	}
}

// TestSkewedProcessClockMovesNoResult proves that the clock of the api
// process changes no result: the watch compares only times of the store. An
// operation that was just started is not unclaimed when the process clock is
// an hour ahead, and one that waits is still reported when the process clock
// is an hour behind.
func TestSkewedProcessClockMovesNoResult(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		skew time.Duration
	}{
		{name: "clock ahead", skew: time.Hour},
		{name: "clock behind", skew: -time.Hour},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			st := openTestStore(t)
			store := st.DurableOperations()
			startOldOperation(t, store)

			api := newTestWatch(store, apiOnly(), beatLease, func() time.Time { return time.Now().Add(tc.skew) })
			api.begin(t.Context())

			_, tracked := workerOf(api)
			claimAttempt(t, tracked)

			report, err := api.WorkerReport(t.Context())
			if err != nil {
				t.Fatalf("report: %v", err)
			}

			// A skew of an hour would show as an age of about an hour.
			if report.Stuck || report.OldestUnclaimedSeconds > time.Minute.Seconds() {
				t.Fatalf("fresh operation, report = %+v, want a live worker and an age far below the skew", report)
			}

			pauseFor(t, beatWait)
			claimAttempt(t, tracked)

			api.mu.Lock()
			api.cachedOK = false
			api.mu.Unlock()

			report, err = api.WorkerReport(t.Context())
			if err != nil {
				t.Fatalf("report: %v", err)
			}

			if report.Stuck || len(report.Operations) != 1 {
				t.Fatalf("old operation, report = %+v, want a live worker and one waiting operation", report)
			}
		})
	}
}

// TestHeartbeatIsRateLimited proves that a worker that polls writes at most
// one heartbeat in a quarter of the lease period.
func TestHeartbeatIsRateLimited(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	api := newTestWatch(st.DurableOperations(), apiOnly(), time.Minute, time.Now)
	worker, tracked := workerOf(api)
	worker.lease = time.Minute

	claimAttempt(t, tracked)

	first, err := st.DurableOperations().LastBeat(t.Context())
	if err != nil || first.At.IsZero() {
		t.Fatalf("LastBeat = %+v, %v; want a beat", first, err)
	}

	claimAttempt(t, tracked)

	second, err := st.DurableOperations().LastBeat(t.Context())
	if err != nil || !second.At.Equal(first.At) {
		t.Fatalf("LastBeat after a second attempt = %+v, %v; want the first beat %v", second, err, first.At)
	}
}

// TestTrackingOpsRecordsAClaim proves that the wrapper of a store that keeps
// operations records a claim attempt as activity and as a heartbeat.
func TestTrackingOpsRecordsAClaim(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	api := newTestWatch(st.DurableOperations(), apiOnly(), beatLease, time.Now)
	worker, _ := workerOf(api)
	ops := trackingOps{Store: st.DurableOperations(), watch: worker}

	_, err := ops.Claim(t.Context(), durable.ClaimRequest{
		Owner: "worker-1", Kinds: []durable.Kind{"none"}, Lease: beatLease, MaxVersions: nil,
		TakeCanceled: false,
	})
	if !errors.Is(err, durable.ErrNoWork) {
		t.Fatalf("claim = %v, want ErrNoWork", err)
	}

	if worker.lastActive.Load() == 0 {
		t.Fatal("the claim attempt did not mark the worker as active")
	}

	beat, err := st.DurableOperations().LastBeat(t.Context())
	if err != nil || beat.At.IsZero() {
		t.Fatalf("LastBeat = %+v, %v; want a beat", beat, err)
	}
}

// TestGateComponentsRefusesHostilePaths proves the component gate alone: a
// process without the api component lets only clean probe and metrics paths
// reach the next handler. Dot segments, double slashes, trailing slashes and
// percent-encoded forms of them get 404, whatever the mux behind does.
func TestGateComponentsRefusesHostilePaths(t *testing.T) {
	t.Parallel()

	reached := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true

		w.WriteHeader(http.StatusOK)
	})

	gate := gateComponents(next, workerOnly())

	for _, tc := range []struct {
		target string
		want   int
	}{
		{"/health/live", http.StatusOK},
		{"/health/ready", http.StatusOK},
		{"/health/ready/worker", http.StatusOK},
		{"/api/server/metrics", http.StatusOK},
		{"/api/server/namespaces", http.StatusNotFound},
		{"/health/ready/../api/server/namespaces", http.StatusNotFound},
		{"/health/ready/%2e%2e/api/server/namespaces", http.StatusNotFound},
		{"/health/ready/%2E%2E/api/server/namespaces", http.StatusNotFound},
		{"/health/ready/..%2fapi/server/namespaces", http.StatusNotFound},
		{"/health/ready%2f..%2fapi/server/namespaces", http.StatusNotFound},
		{"/health/ready/worker/../../../api/server/namespaces", http.StatusNotFound},
		{"//api/server/namespaces", http.StatusNotFound},
		{"//health/ready", http.StatusNotFound},
		{"/health//ready", http.StatusNotFound},
		{"/health/ready//worker", http.StatusNotFound},
		{"/api//server/metrics", http.StatusNotFound},
		{"/api/server/metrics/", http.StatusNotFound},
		{"/api/server/metrics/../namespaces", http.StatusNotFound},
		{"/health/ready/", http.StatusNotFound},
		{"/health/ready/a/b", http.StatusNotFound},
		{"/health/live/../ready", http.StatusNotFound},
		{"/./health/live", http.StatusNotFound},
		{"/health/live/.", http.StatusNotFound},
		{"/health/live%2f..%2f..%2fapi%2fuser", http.StatusNotFound},
		{"/api/server/metrics%2f..%2fnamespaces", http.StatusNotFound},
	} {
		reached = false

		url := "http://example.invalid" + tc.target
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, url, http.NoBody)
		rec := httptest.NewRecorder()

		gate.ServeHTTP(rec, req)

		if rec.Code != tc.want || reached != (tc.want == http.StatusOK) {
			t.Errorf("GET %s: status %d, reached next = %v; want status %d", tc.target, rec.Code, reached, tc.want)
		}
	}
}

// TestGateComponentsPassesEverythingWithAPI proves that a process with the
// api component is not gated.
func TestGateComponentsPassesEverythingWithAPI(t *testing.T) {
	t.Parallel()

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/server/namespaces", http.NoBody)
	rec := httptest.NewRecorder()

	gateComponents(next, AllComponents()).ServeHTTP(rec, req)

	if rec.Code != http.StatusTeapot {
		t.Fatalf("status %d, want the status of the next handler", rec.Code)
	}
}

// TestWorkerLeaseDecidesWhenAWorkerIsAbsent proves that the start gate counts
// 2 lease periods of the worker that wrote the heartbeat, and not 2 lease
// periods of the process that reads it: an api process with a shorter lease
// than its worker admits the start while the worker lives, and an api process
// with a longer lease refuses it as soon as the worker is silent for 2 of its
// own periods.
func TestWorkerLeaseDecidesWhenAWorkerIsAbsent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		apiLease    time.Duration
		workerLease time.Duration
		wantErr     error
	}{
		{"api lease shorter than the worker lease", beatLease, 20 * beatLease, nil},
		{"api lease equal to the worker lease", beatLease, beatLease, noworker.ErrNoWorker},
		{"api lease longer than the worker lease", 20 * beatLease, beatLease, noworker.ErrNoWorker},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			st := openTestStore(t)
			api := newTestWatch(st.DurableOperations(), apiOnly(), tc.apiLease, time.Now)
			api.begin(t.Context())

			worker := newTestWatch(st.DurableOperations(), workerOnly(), tc.workerLease, time.Now)
			worker.owner = "worker-1"
			worker.beatIfDue(t.Context())

			// The worker beat once and is silent from now on. The wait is past
			// 2 periods of the shorter lease and far below 2 of the longer one.
			pauseFor(t, beatWait)

			err := admitStartOf(t, api)
			if !errors.Is(err, tc.wantErr) || (tc.wantErr == nil && err != nil) {
				t.Fatalf("start with the heartbeat of a worker with lease %v read by an api with lease %v: %v, want %v",
					tc.workerLease, tc.apiLease, err, tc.wantErr)
			}
		})
	}
}

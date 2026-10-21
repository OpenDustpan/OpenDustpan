package dustpan

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/server"
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
func admit(t *testing.T, w *workerWatch) error {
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

	if err := admit(t, api); err != nil {
		t.Fatalf("start right after the process began: %v, want it admitted", err)
	}

	pauseFor(t, beatWait)

	err := admit(t, api)
	if !errors.Is(err, server.ErrNoWorker) {
		t.Fatalf("start after 2 leases with no worker: %v, want ErrNoWorker", err)
	}
}

// TestBusyWorkerGivesNoRefusal proves that a worker that holds one long job
// and only renews its claim keeps the gate open, while other work waits
// unclaimed longer than 2 lease periods.
func TestBusyWorkerGivesNoRefusal(t *testing.T) {
	t.Parallel()

	st := openTestStore(t)
	store := st.DurableOperations()
	startOldOperation(t, store)

	api := newTestWatch(store, apiOnly(), beatLease, time.Now)
	api.begin(t.Context())

	_, tracked := workerOf(api)

	claim, err := tracked.Claim(t.Context(), durable.ClaimRequest{
		Owner: "worker-1", Kinds: []durable.Kind{"any"}, Lease: beatLease, MaxVersions: map[durable.Kind]int{"any": 0},
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

		held, err = tracked.Extend(t.Context(), held, beatLease)
		if err != nil {
			t.Fatalf("extend: %v", err)
		}
	}

	if admitErr := admit(t, api); admitErr != nil {
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

// TestRollingUpgradeGivesNoRefusal proves that a due schedule that no worker
// knows, as during a rolling upgrade while only older workers run, shows in
// the report but does not make the gate refuse while the workers beat.
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

	api := newTestWatch(store, apiOnly(), beatLease, time.Now)
	api.specs = func() []durable.ScheduleSpec { return []durable.ScheduleSpec{spec} }
	api.begin(t.Context())

	_, tracked := workerOf(api)

	deadline := time.Now().Add(beatWait + beatLease)

	for time.Now().Before(deadline) {
		claimAttempt(t, tracked)
		pauseFor(t, beatLease/8)
	}

	if err := admit(t, api); err != nil {
		t.Fatalf("start while old workers beat: %v, want it admitted", err)
	}

	report, err := api.WorkerReport(t.Context())
	if err != nil {
		t.Fatalf("report: %v", err)
	}

	if report.Stuck || len(report.Schedules) != 1 || report.Schedules[0].Name != "new-job" {
		t.Fatalf("report = %+v, want a live worker and the due schedule new-job", report)
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

	if err := admit(t, api); err != nil {
		t.Fatalf("start right after a heartbeat: %v, want it admitted", err)
	}

	pauseFor(t, beatWait)

	if err := admit(t, api); !errors.Is(err, server.ErrNoWorker) {
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

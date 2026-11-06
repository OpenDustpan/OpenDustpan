package main

import (
	"net/http"
	"testing"
	"time"
)

func TestRouteOf(t *testing.T) {
	t.Parallel()

	cases := []struct {
		method, path, want string
	}{
		{http.MethodPost, "/api/stacks/load/load", routeStackCreate},
		{http.MethodPost, "/api/stacks/load/load/s1/update", routeUpdateCreate},
		{http.MethodPost, "/api/stacks/load/load/s1/update/abc", routeUpdateStart},
		{http.MethodPatch, "/api/stacks/load/load/s1/update/abc/checkpointverbatim", routeCheckpointSave},
		{http.MethodPost, "/api/stacks/load/load/s1/update/abc/complete", routeUpdateComplete},
		{http.MethodPost, "/api/v2/workspaces/ws-1/actions/lock", routeLock},
		{http.MethodPost, "/api/v2/workspaces/ws-1/actions/unlock", routeUnlock},
		{http.MethodPost, "/api/v2/workspaces/ws-1/state-versions", routeStateDeclare},
		{http.MethodPut, "/api/v2/state-versions/sv-1/content", routeStateUpload},
		{http.MethodGet, "/api/v2/organizations/load/workspaces", routeWorkspaceList},
		{http.MethodPost, "/api/v2/organizations/load/workspaces", routeWorkspaceNew},
		{http.MethodGet, "/api/dustpan/v1/namespaces/load/units", routeInventoryList},
		{http.MethodGet, "/health/ready", routeOther},
	}

	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			t.Parallel()

			if got := routeOf(tc.method, tc.path); got != tc.want {
				t.Fatalf("routeOf = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSummarize(t *testing.T) {
	t.Parallel()

	var samples []sample

	for i := 1; i <= 100; i++ {
		samples = append(samples, sample{
			at: time.Duration(i) * time.Millisecond, route: routeLock,
			took: time.Duration(i) * time.Millisecond, status: http.StatusOK, failed: false,
		})
	}

	samples = append(samples, sample{at: 0, route: routeUnlock, took: time.Millisecond, status: 0, failed: true})

	got := summarize(samples, 10*time.Second)

	if got.Requests != 101 || got.Errors != 1 || got.Statuses["transport"] != 1 {
		t.Fatalf("counts = %d requests, %d errors, %v", got.Requests, got.Errors, got.Statuses)
	}

	if got.RequestsPerSec != 10.1 {
		t.Fatalf("requests per second = %v, want 10.1", got.RequestsPerSec)
	}

	if len(got.Routes) != 2 || got.Routes[0].Route != routeLock {
		t.Fatalf("routes = %+v", got.Routes)
	}

	lock := got.Routes[0]
	if lock.P50Ms != 51 || lock.P95Ms != 95 || lock.P99Ms != 99 || lock.MaxMs != 100 {
		t.Fatalf("lock percentiles = %+v", lock)
	}
}

func TestBalancerPickSkipsUnhealthy(t *testing.T) {
	t.Parallel()

	procs := []*serverProc{
		newServerProc(
			procSpec{
				index:        0,
				port:         1,
				bin:          "x",
				args:         nil,
				env:          nil,
				dir:          t.TempDir(),
				startTimeout: 0,
				stopTimeout:  0,
			},
		),
		newServerProc(
			procSpec{
				index:        1,
				port:         2,
				bin:          "x",
				args:         nil,
				env:          nil,
				dir:          t.TempDir(),
				startTimeout: 0,
				stopTimeout:  0,
			},
		),
		newServerProc(
			procSpec{
				index:        2,
				port:         3,
				bin:          "x",
				args:         nil,
				env:          nil,
				dir:          t.TempDir(),
				startTimeout: 0,
				stopTimeout:  0,
			},
		),
	}

	lb, err := newBalancer(procs)
	if err != nil {
		t.Fatal(err)
	}

	if got := lb.pick(); got != -1 {
		t.Fatalf("pick with no healthy instance = %d, want -1", got)
	}

	procs[0].healthy.Store(true)
	procs[2].healthy.Store(true)

	seen := map[int]int{}

	for range 100 {
		seen[lb.pick()]++
	}

	if seen[1] != 0 || seen[0] != 50 || seen[2] != 50 {
		t.Fatalf("picks = %v, want an even split over instances 0 and 2", seen)
	}
}

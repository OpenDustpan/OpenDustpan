package runnerproxysvc_test

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/runnerproxysvc"
	"opendustpan.org/opendustpan/pkg/runner"
)

// TestProxyIsOnlyExit checks that a claim with proxy rules gets one proxy and
// a sandbox route to that proxy only. No claim gets a direct route.
func TestProxyIsOnlyExit(t *testing.T) {
	t.Parallel()

	for _, isolation := range []runner.Isolation{
		runner.IsolationFirecracker, runner.IsolationGVisor, runner.IsolationKubernetes,
	} {
		t.Run(string(isolation), func(t *testing.T) {
			t.Parallel()

			checkProxyIsOnlyExit(t, isolation)
		})
	}
}

// checkProxyIsOnlyExit checks the placement and the routes of two claims.
func checkProxyIsOnlyExit(t *testing.T, isolation runner.Isolation) {
	t.Helper()

	place, err := runnerproxysvc.Plan(isolation, 1)
	if err != nil {
		t.Fatalf("Plan(%s): %v", isolation, err)
	}

	if place.Proxies != 1 || place.Route != runnerproxysvc.RouteProxyOnly || !place.DropOther {
		t.Fatalf("Plan(%s) = %+v, want one proxy on a proxy-only route with other packets dropped",
			isolation, place)
	}

	fakes := newCoordinatorFakes()
	coord := newTestCoordinator(t, fakes.config())

	claimA := testClaim("claim-a", isolation, 1)
	claimB := testClaim("claim-b", isolation, 1)

	startClaim(t, coord, claimA)
	startClaim(t, coord, claimB)

	checkRouteCalls(t, fakes, claimA, claimB)
	endClaim(t, coord, claimA)
	endClaim(t, coord, claimB)
}

// checkRouteCalls checks the two proxy-only routes of the fakes.
func checkRouteCalls(t *testing.T, fakes *coordinatorFakes, claims ...runnerproxysvc.Claim) {
	t.Helper()

	if got := fakes.startCount(); got != len(claims) {
		t.Fatalf("the coordinator started %d proxies, want one for each of %d claims", got, len(claims))
	}

	routes := fakes.routeCalls()
	if len(routes) != len(claims) {
		t.Fatalf("the coordinator set %d routes, want one for each of %d claims", len(routes), len(claims))
	}

	for i := range routes {
		route := &routes[i]
		want := fakes.addrOf(route.claim.ID)
		if route.addr != want {
			t.Errorf("route of %s = %s, want the proxy address %s", route.claim.ID, route.addr, want)
		}

		if !route.proxyOnly {
			t.Errorf("route of %s is not proxy only", route.claim.ID)
		}
	}
}

// TestProcessIsolationWithoutProxyBoundary checks that process isolation
// never runs a claim with proxy rules: it has no kernel boundary.
func TestProcessIsolationWithoutProxyBoundary(t *testing.T) {
	t.Parallel()

	if _, err := runnerproxysvc.Plan(runner.IsolationProcess, 1); !errors.Is(err, runnerproxysvc.ErrPlacement) {
		t.Fatalf("Plan(process, 1) error = %v, want ErrPlacement", err)
	}

	fakes := newCoordinatorFakes()
	coord := newTestCoordinator(t, fakes.config())

	claim := testClaim("claim-process", runner.IsolationProcess, 1)
	if err := coord.Start(t.Context(), claim); !errors.Is(err, runnerproxysvc.ErrPlacement) {
		t.Fatalf("Start(process) error = %v, want ErrPlacement", err)
	}

	if got := fakes.startCount(); got != 0 {
		t.Fatalf("the coordinator started %d proxies for process isolation", got)
	}

	failure := fakes.waitFailure(t)

	if !errors.Is(failure.reason, runnerproxysvc.ErrPlacement) {
		t.Errorf("the failure reason %v, want ErrPlacement", failure.reason)
	}
}

// routeCall is one route of a claim.
type routeCall struct {
	claim     runnerproxysvc.Claim
	addr      string
	proxyOnly bool
}

// failure is one failed claim.
type failure struct {
	claim  runnerproxysvc.Claim
	reason error
}

// coordinatorFakes records the calls of the coordinator.
type coordinatorFakes struct {
	mu      sync.Mutex
	procs   []*processFake
	routes  []routeCall
	removed []runnerproxysvc.Claim
	fails   chan failure
}

// processFake is one started process of the fakes.
type processFake struct {
	claimID string
	addr    string
	done    chan struct{}
	err     error
}

// newCoordinatorFakes returns the fakes of one coordinator.
func newCoordinatorFakes() *coordinatorFakes {
	return &coordinatorFakes{
		mu: sync.Mutex{}, procs: nil, routes: nil, removed: nil,
		fails: make(chan failure, 8),
	}
}

// config returns the coordinator configuration of the fakes.
func (f *coordinatorFakes) config() runnerproxysvc.CoordinatorConfig {
	return runnerproxysvc.CoordinatorConfig{
		Start: f.start, Route: f.route, RemoveRoute: f.removeRoute, Fail: f.fail,
	}
}

// start returns a new fake process for claim.
func (f *coordinatorFakes) start(_ context.Context, claim runnerproxysvc.Claim) (*runnerproxysvc.Process, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	proc := &processFake{
		claimID: claim.ID, addr: "proxy-" + strconv.Itoa(len(f.procs)+1),
		done: make(chan struct{}), err: nil,
	}
	f.procs = append(f.procs, proc)

	return runnerproxysvc.NewProcess(proc.addr, proc.done, func() error { return proc.err },
		func(context.Context) error { return nil }), nil
}

// route records the proxy-only route of a claim.
func (f *coordinatorFakes) route(_ context.Context, claim runnerproxysvc.Claim, addr string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.routes = append(f.routes, routeCall{claim: claim, addr: addr, proxyOnly: true})

	return nil
}

// removeRoute records the removed route of a claim.
func (f *coordinatorFakes) removeRoute(_ context.Context, claim runnerproxysvc.Claim) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.removed = append(f.removed, claim)

	return nil
}

// fail records a failed claim and wakes a waiting test.
func (f *coordinatorFakes) fail(_ context.Context, claim runnerproxysvc.Claim, reason error) {
	f.fails <- failure{claim: claim, reason: reason}
}

// startCount returns the number of started processes.
func (f *coordinatorFakes) startCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return len(f.procs)
}

// procOf returns the latest process of a claim.
func (f *coordinatorFakes) procOf(t *testing.T, claimID string) *processFake {
	t.Helper()

	f.mu.Lock()
	defer f.mu.Unlock()

	var found *processFake

	for _, proc := range f.procs {
		if proc.claimID == claimID {
			found = proc
		}
	}

	if found == nil {
		t.Fatalf("no proxy started for claim %s", claimID)
	}

	return found
}

// routeCalls returns the routes in order.
func (f *coordinatorFakes) routeCalls() []routeCall {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]routeCall(nil), f.routes...)
}

// removedCount returns the number of removed routes.
func (f *coordinatorFakes) removedCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return len(f.removed)
}

// addrOf returns the address of the first process of a claim, or an empty
// string.
func (f *coordinatorFakes) addrOf(claimID string) string {
	f.mu.Lock()
	defer f.mu.Unlock()

	for _, proc := range f.procs {
		if proc.claimID == claimID {
			return proc.addr
		}
	}

	return ""
}

// waitFailure waits for one failure of the fake failer.
func (f *coordinatorFakes) waitFailure(t *testing.T) failure {
	t.Helper()

	select {
	case got := <-f.fails:
		return got
	case <-time.After(5 * time.Second):
		t.Fatal("the coordinator did not fail the claim")

		return failure{claim: testClaim("none", runner.IsolationGVisor, 0), reason: nil}
	}
}

// newTestCoordinator returns a coordinator with cfg or fails the test.
func newTestCoordinator(t *testing.T, cfg runnerproxysvc.CoordinatorConfig) *runnerproxysvc.Coordinator {
	t.Helper()

	coord, err := runnerproxysvc.NewCoordinator(cfg)
	if err != nil {
		t.Fatalf("NewCoordinator: %v", err)
	}

	return coord
}

// startClaim starts claim and fails the test on an error.
func startClaim(t *testing.T, coord *runnerproxysvc.Coordinator, claim runnerproxysvc.Claim) {
	t.Helper()

	if err := coord.Start(t.Context(), claim); err != nil {
		t.Fatalf("Start(%s): %v", claim.ID, err)
	}
}

// endClaim ends claim and fails the test on an error.
func endClaim(t *testing.T, coord *runnerproxysvc.Coordinator, claim runnerproxysvc.Claim) {
	t.Helper()

	if err := coord.End(t.Context(), claim); err != nil {
		t.Fatalf("End(%s): %v", claim.ID, err)
	}
}

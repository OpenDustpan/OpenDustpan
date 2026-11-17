package runnerproxysvc_test

import (
	"context"
	"errors"
	"testing"

	"opendustpan.org/opendustpan/internal/runnerproxysvc"
	"opendustpan.org/opendustpan/pkg/runner"
)

// errProxyStart is the error of a proxy process that does not start.
var errProxyStart = errors.New("the proxy process did not start")

// TestRouteStaysOnLoss checks that a proxy loss fails the claim closed and
// keeps the proxy-only route: a restart routes to the new proxy and never
// opens a direct path.
func TestRouteStaysOnLoss(t *testing.T) {
	t.Parallel()

	fakes := newCoordinatorFakes()

	coord, err := runnerproxysvc.NewCoordinator(fakes.config())
	if err != nil {
		t.Fatalf("NewCoordinator: %v", err)
	}

	claim := testClaim("claim-loss", runner.IsolationGVisor, 1)
	if err := coord.Start(t.Context(), claim); err != nil {
		t.Fatalf("Start: %v", err)
	}

	first := fakes.procOf(t, claim.ID)
	close(first.done)

	failure := fakes.waitFailure(t)
	if !errors.Is(failure.reason, runnerproxysvc.ErrLost) {
		t.Errorf("the failure reason %v, want ErrLost", failure.reason)
	}

	if got := fakes.removedCount(); got != 0 {
		t.Errorf("the coordinator removed %d routes on a proxy loss, want the proxy-only route kept", got)
	}

	if err := coord.Start(t.Context(), claim); err != nil {
		t.Fatalf("Start after the loss: %v", err)
	}

	routes := fakes.routeCalls()
	if len(routes) != 2 {
		t.Fatalf("the coordinator set %d routes, want a second proxy-only route after the restart", len(routes))
	}

	if routes[0].addr == routes[1].addr {
		t.Errorf("the restart routed to the old proxy address %s", routes[0].addr)
	}

	if !routes[1].proxyOnly {
		t.Error("the route after the restart is not proxy only")
	}

	if err := coord.End(t.Context(), claim); err != nil {
		t.Fatalf("End: %v", err)
	}

	if got := fakes.removedCount(); got != 1 {
		t.Errorf("the coordinator removed %d routes at the end of the claim, want 1", got)
	}
}

// TestStartFailureFailsClaim checks that a proxy which cannot start fails
// the claim before the coordinator sets a route.
func TestStartFailureFailsClaim(t *testing.T) {
	t.Parallel()

	errStart := errProxyStart
	fakes := newCoordinatorFakes()

	cfg := fakes.config()
	cfg.Start = func(context.Context, runnerproxysvc.Claim) (*runnerproxysvc.Process, error) {
		return nil, errStart
	}

	coord, err := runnerproxysvc.NewCoordinator(cfg)
	if err != nil {
		t.Fatalf("NewCoordinator: %v", err)
	}

	claim := testClaim("claim-start", runner.IsolationKubernetes, 1)

	err = coord.Start(t.Context(), claim)
	if !errors.Is(err, runnerproxysvc.ErrStart) || !errors.Is(err, errStart) {
		t.Fatalf("Start error = %v, want ErrStart and the start error", err)
	}

	failure := fakes.waitFailure(t)
	if !errors.Is(failure.reason, runnerproxysvc.ErrStart) || !errors.Is(failure.reason, errStart) {
		t.Errorf("the failure reason %v, want ErrStart and the start error", failure.reason)
	}

	if got := len(fakes.routeCalls()); got != 0 {
		t.Errorf("the coordinator set %d routes after a failed start", got)
	}
}

package runnerproxysvc_test

import (
	"testing"

	"opendustpan.org/opendustpan/internal/runnerproxysvc"
	"opendustpan.org/opendustpan/pkg/runner"
)

// TestNoProxyWithoutRules checks the opt-in rule: a unit with no proxy rules
// runs with no job proxy and its sandbox routing stays unchanged.
func TestNoProxyWithoutRules(t *testing.T) {
	t.Parallel()

	for _, isolation := range []runner.Isolation{
		runner.IsolationFirecracker, runner.IsolationGVisor,
		runner.IsolationKubernetes, runner.IsolationProcess,
	} {
		t.Run(string(isolation), func(t *testing.T) {
			t.Parallel()

			checkNoProxyWithoutRules(t, isolation)
		})
	}
}

// checkNoProxyWithoutRules checks one isolation kind with no proxy rules.
func checkNoProxyWithoutRules(t *testing.T, isolation runner.Isolation) {
	t.Helper()

	place, err := runnerproxysvc.Plan(isolation, 0)
	if err != nil {
		t.Fatalf("Plan(%s, 0): %v", isolation, err)
	}

	if place.Proxies != 0 || place.Route != runnerproxysvc.RouteUnchanged || place.DropOther {
		t.Fatalf("Plan(%s, 0) = %+v, want no proxy and an unchanged route", isolation, place)
	}

	fakes := newCoordinatorFakes()
	coord := newTestCoordinator(t, fakes.config())

	claim := testClaim("claim-optin", isolation, 0)
	if err := coord.Start(t.Context(), claim); err != nil {
		t.Fatalf("Start(%s): %v", claim.ID, err)
	}

	if got := fakes.startCount(); got != 0 {
		t.Errorf("the coordinator started %d proxies for a unit with no proxy rules", got)
	}

	if got := len(fakes.routeCalls()); got != 0 {
		t.Errorf("the coordinator set %d routes for a unit with no proxy rules", got)
	}

	if got := fakes.removedCount(); got != 0 {
		t.Errorf("the coordinator removed %d routes for a unit with no proxy rules", got)
	}
}

package runnerproxysvc

import (
	"fmt"

	"opendustpan.org/opendustpan/pkg/runner"
)

// Route is the network route of a claim sandbox.
type Route string

// The routes of a claim sandbox.
const (
	// RouteUnchanged leaves the sandbox route as it is.
	RouteUnchanged Route = "unchanged"
	// RouteProxyOnly sends every packet of the sandbox to the proxy and drops
	// each other packet, DNS included.
	RouteProxyOnly Route = "proxy-only"
)

// Placement is the place of the job proxy of one claim.
type Placement struct {
	// Proxies is the number of job proxy processes of the claim.
	Proxies int
	// Route is the route of the sandbox.
	Route Route
	// DropOther drops every packet of the sandbox that is not for the proxy.
	DropOther bool
	// HostLevel reports whether the coordinator routes the host network of
	// the sandbox (firecracker, gvisor) rather than the network of one claim
	// (kubernetes).
	HostLevel bool
}

// Plan returns the placement of a claim with the isolation kind k. rules is
// the number of proxy rules in the joined set of the unit; zero means the
// unit has no proxy rules. A claim with no proxy rules gets no proxy and its
// route stays unchanged. A claim with proxy rules gets one proxy. Process
// isolation has no kernel boundary, so it refuses the claim.
func Plan(k runner.Isolation, rules int) (Placement, error) {
	if !k.Valid() {
		return Placement{}, fmt.Errorf("%w: unknown isolation %q", ErrPlacement, k)
	}

	if rules <= 0 {
		return Placement{Proxies: 0, Route: RouteUnchanged, DropOther: false, HostLevel: false}, nil
	}

	switch k {
	case runner.IsolationFirecracker, runner.IsolationGVisor:
		return Placement{Proxies: 1, Route: RouteProxyOnly, DropOther: true, HostLevel: true}, nil
	case runner.IsolationKubernetes:
		return Placement{Proxies: 1, Route: RouteProxyOnly, DropOther: true, HostLevel: false}, nil
	case runner.IsolationProcess:
		return Placement{}, fmt.Errorf("%w: process isolation has no kernel boundary for a claim with proxy rules",
			ErrPlacement)
	default:
		return Placement{}, fmt.Errorf("%w: unknown isolation %q", ErrPlacement, k)
	}
}

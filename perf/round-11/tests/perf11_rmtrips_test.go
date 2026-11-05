package servertest_test

import (
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/dustpan"
)

// TestPerf11RmTrips counts the round trips of a Pulumi stack rm and prints each statement.
func TestPerf11RmTrips(t *testing.T) {
	h := newRoundTripHarness(t, traceRequest, dustpan.WithPublicURL("https://dustpan.example.com"))
	h.createStack("base")
	for i := range 5 {
		name := "rm" + string(rune('a'+i))
		h.createStack(name)
		h.measure(func() { h.call(http.MethodDelete, "/api/stacks/acme/web/"+name, "", http.StatusNoContent) })
	}
	trips := withoutTimerReads(withoutPrepares(h.trips))
	t.Logf("PERF11 stack-rm round trips: %d", len(trips))
	for i, r := range trips {
		t.Logf("PERF11   %2d %s %s", i+1, r.kind, strings.Join(strings.Fields(r.sql), " "))
	}
}

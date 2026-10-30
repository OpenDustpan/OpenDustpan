package dustpan_test

import (
	"slices"
	"testing"

	"opendustpan.org/opendustpan/internal/api/apidesc"
)

// TestUnitGateRoutesShareAMuxWithTheTriggerRoutes proves that the routes of the
// gates and the access limits of a unit by tool and name, and the routes of the
// triggers of a unit by its stable ID, live on one mux. A unit path of the first
// family that had the same number of segments as one of the second family
// would make the server fail at start, because neither pattern is more
// specific than the other.
func TestUnitGateRoutesShareAMuxWithTheTriggerRoutes(t *testing.T) {
	t.Parallel()

	const (
		unitBase    = "/api/dustpan/v1/namespaces/{namespace}/units/"
		byName      = unitBase + "by-name/{tool}/{name}/"
		triggerPath = unitBase + "{unit_id}/triggers/{trigger_id}"
	)

	served := newEveryModuleServer(t).Operations()

	want := []struct{ method, path string }{
		{"GET", byName + "gates"},
		{"PUT", byName + "gates/{id}"},
		{"GET", byName + "access-limits"},
		{"DELETE", byName + "access-limits/{id}"},
		{"GET", triggerPath},
		{"DELETE", triggerPath},
	}

	for _, w := range want {
		found := slices.ContainsFunc(served, func(o apidesc.Operation) bool {
			return o.Method == w.method && o.Path == w.path
		})
		if !found {
			t.Errorf("the server does not serve %s %s", w.method, w.path)
		}
	}
}

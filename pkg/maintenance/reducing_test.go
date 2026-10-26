package maintenance_test

import (
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/maintenance"
)

// TestReducingRoutesList proves the shape of the list: each entry has a
// method, a pattern, at least one action and a known kind, no entry
// repeats, and every kind that the list names is a journal kind.
func TestReducingRoutesList(t *testing.T) {
	t.Parallel()

	seen := map[string]bool{}

	for _, r := range maintenance.ReducingRoutes() {
		key := r.Method + " " + r.Pattern
		t.Run(key, func(t *testing.T) {
			t.Parallel()

			if r.Method == "" || !strings.HasPrefix(r.Pattern, "/") || len(r.Actions) == 0 {
				t.Fatalf("entry %+v is missing a method, a pattern or an action", r)
			}

			if slices.Contains(r.Actions, "") {
				t.Fatalf("entry %s has an empty action", key)
			}

			if !slices.Contains(maintenance.Kinds(), r.Kind) {
				t.Fatalf("entry %s has the kind %q, which is not a journal kind", key, r.Kind)
			}
		})

		if seen[key] {
			t.Errorf("entry %s is on the list twice", key)
		}

		seen[key] = true
	}
}

// TestKindsWithoutRoute names the journal kinds that no listed route writes,
// each with its reason. A kind that loses its route, or a new kind, fails
// here until this table says why.
func TestKindsWithoutRoute(t *testing.T) {
	t.Parallel()

	withoutRoute := map[string]string{
		maintenance.RevokedTempAdmin: "a temporary administrator ends by expiry, not by a request",
	}

	routed := map[string]bool{}
	for _, r := range maintenance.ReducingRoutes() {
		routed[r.Kind] = true
	}

	for _, kind := range maintenance.Kinds() {
		if _, ok := withoutRoute[kind]; ok == routed[kind] {
			t.Errorf("kind %q: routed=%v, in the table of kinds without a route=%v", kind, routed[kind], ok)
		}
	}
}

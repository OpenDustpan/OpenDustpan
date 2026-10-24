package notifications_test

import (
	"testing"

	"opendustpan.org/opendustpan/internal/api/route"
)

// TestNoRouteIsPublic proves that the description of this API matches what
// it enforces: every route needs sign-in, and no operation reads as public.
func TestNoRouteIsPublic(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	for _, rt := range f.handler.Routes() {
		if rt.Scope != route.ScopeAuthorized {
			t.Errorf("route %s %s has the scope %d, want ScopeAuthorized", rt.Method, rt.Path, rt.Scope)
		}
	}

	for _, op := range f.handler.Operations() {
		if op.Public {
			t.Errorf("operation %s %s is described as public", op.Method, op.Path)
		}
	}
}

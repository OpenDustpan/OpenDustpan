package notifications_test

import (
	"testing"

	apinotifications "opendustpan.org/opendustpan/internal/api/notifications"
)

// TestNoRouteIsPublic proves that the description of this API matches what
// it enforces: every route needs sign-in, and no operation reads as public.
func TestNoRouteIsPublic(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	for _, route := range f.handler.Routes() {
		if route.Scope != apinotifications.ScopeAuthorized {
			t.Errorf("route %s %s has the scope %d, want ScopeAuthorized", route.Method, route.Path, route.Scope)
		}
	}

	for _, op := range f.handler.Operations() {
		if op.Public {
			t.Errorf("operation %s %s is described as public", op.Method, op.Path)
		}
	}
}

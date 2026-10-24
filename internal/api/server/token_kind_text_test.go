package server_test

import (
	"net/http"
	"strings"
	"testing"
)

// TestPersonalTokenOnAServiceRouteNamesTheRightRoute proves that a create of
// a token for a person on the route of service principals answers 400 with a
// text that names the route that makes a personal token, and not a route that
// has no create.
func TestPersonalTokenOnAServiceRouteNamesTheRightRoute(t *testing.T) {
	t.Parallel()

	f := newAccessFixture(t)

	var doc errorDoc

	f.as(t, "admin", apiCall{
		http.MethodPost, "/api/server/principals/prn-alice/tokens", `{"name":"ci"}`, http.StatusBadRequest,
	}, &doc)

	if !strings.Contains(doc.Message, "/api/server/caller/tokens") ||
		strings.HasSuffix(doc.Message, "goes through /api/server/tokens") {
		t.Fatalf("message = %q, want the route /api/server/caller/tokens", doc.Message)
	}
}

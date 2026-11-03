package runnerapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"opendustpan.org/opendustpan/internal/requestid"
)

// TestPatternMuxStoresTheRoute checks that each route of the mux carries its
// constant pattern in the request context, so a failure log line names the
// route and never the path of the request.
func TestPatternMuxStoresTheRoute(t *testing.T) {
	t.Parallel()

	const pattern = "GET /probe/{name}"

	var got string

	mux := newPatternMux()
	mux.HandleFunc(pattern, func(_ http.ResponseWriter, r *http.Request) {
		got = requestid.RouteFromContext(r.Context())
	})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/probe/secret-name", http.NoBody)
	mux.ServeHTTP(httptest.NewRecorder(), req)

	if got != pattern {
		t.Errorf("route = %q, want %q", got, pattern)
	}
}

package server

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

	var handled string

	mux.Handle("GET /handled/{name}", http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		handled = requestid.RouteFromContext(r.Context())
	}))

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/probe/secret-name", http.NoBody)
	mux.ServeHTTP(httptest.NewRecorder(), req)

	if got != pattern {
		t.Errorf("route = %q, want %q", got, pattern)
	}

	mux.ServeHTTP(httptest.NewRecorder(),
		httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/handled/secret-name", http.NoBody))

	if handled != "GET /handled/{name}" {
		t.Errorf("route of Handle = %q, want the pattern", handled)
	}
}

package route_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"opendustpan.org/opendustpan/internal/api/route"
	"opendustpan.org/opendustpan/internal/requestid"
)

// TestMuxStoresTheRoute checks that each route of the mux carries its constant
// pattern in the request context, so a failure log line names the route and
// never the path of the request.
func TestMuxStoresTheRoute(t *testing.T) {
	t.Parallel()

	var viaFunc, viaHandle string

	mux := route.NewMux()
	mux.HandleFunc("GET /func/{name}", func(_ http.ResponseWriter, r *http.Request) {
		viaFunc = requestid.RouteFromContext(r.Context())
	})
	mux.Handle("GET /handle/{name}", http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		viaHandle = requestid.RouteFromContext(r.Context())
	}))

	for _, path := range []string{"/func/secret", "/handle/secret"} {
		mux.ServeHTTP(
			httptest.NewRecorder(),
			httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, http.NoBody),
		)
	}

	if viaFunc != "GET /func/{name}" || viaHandle != "GET /handle/{name}" {
		t.Errorf("routes = %q, %q, want the patterns", viaFunc, viaHandle)
	}
}

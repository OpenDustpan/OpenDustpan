package requestid_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"opendustpan.org/opendustpan/internal/requestid"
)

func TestNewMintDistinctIDs(t *testing.T) {
	t.Parallel()

	if first, second := requestid.New(), requestid.New(); first == "" || first == second {
		t.Fatalf("IDs = %q, %q, want two distinct non-empty IDs", first, second)
	}
}

func TestFromContextRoundTrip(t *testing.T) {
	t.Parallel()

	ctx := requestid.WithContext(t.Context(), "req-1")
	if got := requestid.FromContext(ctx); got != "req-1" {
		t.Fatalf("ID = %q, want req-1", got)
	}

	if got := requestid.FromContext(t.Context()); got != "" {
		t.Fatalf("ID = %q, want empty", got)
	}
}

func TestWithRouteStoresThePattern(t *testing.T) {
	t.Parallel()

	var got string

	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = requestid.RouteFromContext(r.Context())
	})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/x/secret", http.NoBody)
	requestid.WithRoute("GET /x/{name}", next).ServeHTTP(httptest.NewRecorder(), req)

	if got != "GET /x/{name}" {
		t.Fatalf("route = %q, want the pattern", got)
	}

	if got := requestid.RouteFromContext(t.Context()); got != "" {
		t.Fatalf("route = %q, want empty", got)
	}
}

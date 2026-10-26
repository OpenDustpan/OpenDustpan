package dustpan

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestIsReducingRunnerRoute sends hostile requests straight to the check of
// the gate, as if the router before it had failed: only the two reducing
// routes with a canonical path pass.
func TestIsReducingRunnerRoute(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		method string
		path   string
		want   bool
	}{
		{"retire", http.MethodPost, "/api/runners/acme/runners/rn-1/retire", true},
		{"revoke", http.MethodPost, "/api/runners/acme/pools/pool-1/join-tokens/rjt-1/revoke", true},
		{"retire with GET", http.MethodGet, "/api/runners/acme/runners/rn-1/retire", false},
		{"drain", http.MethodPost, "/api/runners/acme/runners/rn-1/drain", false},
		{"pools", http.MethodPost, "/api/runners/acme/pools", false},
		{"retire with dot segments", http.MethodPost, "/api/runners/acme/runners/../runners/rn-1/retire", false},
		{"retire with a double slash", http.MethodPost, "/api/runners/acme//runners/rn-1/retire", false},
		{"retire with a trailing path", http.MethodPost, "/api/runners/acme/runners/rn-1/retire/extra", false},
		{"retire under another prefix", http.MethodPost, "/api/other/acme/runners/rn-1/retire", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequestWithContext(t.Context(), tc.method, "/", nil)
			req.URL.Path = tc.path

			if got := isReducingRunnerRoute(req); got != tc.want {
				t.Fatalf("isReducingRunnerRoute(%s %s) = %v, want %v", tc.method, tc.path, got, tc.want)
			}
		})
	}
}

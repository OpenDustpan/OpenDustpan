package dustpan_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
)

// TestRouteErrorsAreNoStore proves that each answer that the SDK writes by
// itself, a 405 of a served route and a 404 of an unknown path, carries
// Cache-Control: no-store. A cached error would outlive the state that
// caused it.
func TestRouteErrorsAreNoStore(t *testing.T) {
	t.Parallel()

	handler := newEveryModuleServer(t).Handler()

	for _, tc := range []struct {
		name, method, path string
		status             int
		code               string
	}{
		{
			"jobs", http.MethodPatch, "/api/dustpan/v1/namespaces/acme/jobs/job-1",
			http.StatusMethodNotAllowed, errcode.ServerMethodNotAllowed,
		},
		{
			"drift", http.MethodPut, "/api/dustpan/v1/namespaces/acme/units/unit-1/drift",
			http.StatusMethodNotAllowed, errcode.ServerMethodNotAllowed,
		},
		{
			"trigger fire", http.MethodPut, "/api/dustpan/v1/namespaces/acme/units/unit-1/triggers/trigger-1/fire",
			http.StatusMethodNotAllowed, errcode.ServerMethodNotAllowed,
		},
		{
			"runner pools", http.MethodPatch, "/api/runners/acme/pools",
			http.StatusMethodNotAllowed, errcode.ServerMethodNotAllowed,
		},
		{
			"inventory", http.MethodGet, "/api/inventory/deleted-units/pulumi/unit-1/restore",
			http.StatusMethodNotAllowed, errcode.ServerMethodNotAllowed,
		},
		{
			"audit", http.MethodDelete, "/api/audit/entries",
			http.StatusMethodNotAllowed, errcode.ServerMethodNotAllowed,
		},
		{
			"server", http.MethodPost, "/api/server/metrics",
			http.StatusMethodNotAllowed, errcode.ServerMethodNotAllowed,
		},
		{
			"router 404", http.MethodGet, "/api/dustpan/v1/no-such-route",
			http.StatusNotFound, errcode.ServerRouteNotFound,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequestWithContext(t.Context(), tc.method, tc.path, http.NoBody)
			req.Header.Set("Authorization", "Bearer "+adminToken)

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.status, rec.Body.String())
			}

			if got := rec.Header().Get("Cache-Control"); got != "no-store" {
				t.Fatalf("Cache-Control = %q, want no-store: %s", got, rec.Body.String())
			}

			errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, tc.status, rec.Body.Bytes(),
				errcodetest.Want{Code: tc.code, Args: nil, RequestBody: nil})
		})
	}
}

package dustpan_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
)

// TestToolNeutralMethodNotAllowedIsJSON proves that a method that a route of a
// tool-neutral API does not serve answers 405 in the JSON error shape of that
// API, with server.method-not-allowed and the Allow header, and not as plain
// text.
func TestToolNeutralMethodNotAllowedIsJSON(t *testing.T) {
	t.Parallel()

	handler := startedHandler(t, newEveryModuleServer(t))

	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/api/dustpan/v1/namespaces/acme/units/unit-1/outputs"},
		{http.MethodGet, "/api/dustpan/v1/namespaces/acme/jobs"},
		{http.MethodPost, "/api/server/tokens"},
		{http.MethodGet, "/api/inventory/deleted-units/pulumi/unit-1/restore"},
		{http.MethodGet, "/api/runners/acme/runners/run-1/drain"},
		{http.MethodDelete, "/api/audit/entries"},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequestWithContext(t.Context(), tc.method, tc.path, http.NoBody)
			req.Header.Set("Authorization", "Bearer "+adminToken)

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if got := rec.Header().Get("Content-Type"); got != "application/json" {
				t.Fatalf("Content-Type = %q, want application/json: %d %s", got, rec.Code, rec.Body.String())
			}

			if rec.Header().Get("Allow") == "" {
				t.Fatalf("the answer has no Allow header: %s", rec.Body.String())
			}

			errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, http.StatusMethodNotAllowed, rec.Body.Bytes(),
				errcodetest.Want{Code: errcode.ServerMethodNotAllowed, Args: nil, RequestBody: nil})
		})
	}
}

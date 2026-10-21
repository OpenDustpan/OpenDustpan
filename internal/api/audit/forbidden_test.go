package audit_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	apitaudit "opendustpan.org/opendustpan/internal/api/audit"
	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
)

// TestForbiddenCodes proves that each cause of a 403 keeps its message and
// status and adds its machine code, on every authorized route.
func TestForbiddenCodes(t *testing.T) {
	t.Parallel()

	for _, route := range newTestHandler(t).Routes() {
		if route.Scope == apitaudit.ScopePublic {
			continue
		}

		for _, cause := range errcodetest.Causes(t) {
			t.Run(route.Method+" "+route.Path+" "+cause.Name, func(t *testing.T) {
				t.Parallel()

				h, _ := newEnforcingHandler(t, errcodetest.Authorizer{Err: cause.Err})
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, routeRequest(t, route, "test-token"))

				if rec.Code != http.StatusForbidden {
					t.Fatalf("status = %d %s, want 403", rec.Code, rec.Body.String())
				}

				errcodetest.Check(t, rec.Code, rec.Body.Bytes(), cause)
			})
		}
	}
}

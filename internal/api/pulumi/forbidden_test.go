package pulumi_test

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"opendustpan.org/opendustpan/internal/api/pulumi"
	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
)

// TestForbiddenCodes proves that each cause of a 403 keeps its message and
// status and adds its machine code, on every authorized route that
// TestRoutesEnforceAuthorization covers.
func TestForbiddenCodes(t *testing.T) {
	t.Parallel()

	skipped := slices.Concat(updatePatterns(), secretPatterns(), statePatterns(), revealPatterns(), journalPatterns())

	for _, route := range newTestHandler(t).Routes() {
		if route.Scope == pulumi.ScopePublic || slices.Contains(skipped, route.Method+" "+route.Path) {
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

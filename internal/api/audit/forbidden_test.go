package audit_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	apitaudit "opendustpan.org/opendustpan/internal/api/audit"
	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
	"opendustpan.org/opendustpan/pkg/identity"
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

// TestNoInstallationActionHasNoCode proves that a route with no installation
// action refuses the installation object with 403 and no machine code, as the
// refusal of the server routes does. No row of the route table is such a
// route, so the test gives the wrapper one.
func TestNoInstallationActionHasNoCode(t *testing.T) {
	t.Parallel()

	h, _ := newEnforcingHandler(t, errcodetest.Authorizer{Err: nil})
	route := apitaudit.Route{
		Method: http.MethodGet, Path: "/x", Scope: apitaudit.ScopeAuthorized,
		Actions:       []identity.Action{identity.ActionNamespaceReadAudit},
		ServerActions: nil,
		Resolve: func(http.ResponseWriter, *http.Request) (identity.Object, error) {
			return identity.ServerObject(), nil
		},
		Serve: func(http.ResponseWriter, *http.Request, identity.Principal, identity.Object) {
			t.Error("the route served a request that has no installation action")
		},
	}

	rec := httptest.NewRecorder()
	h.Enforce(route).ServeHTTP(rec, routeRequest(t, route, "test-token"))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", rec.Code, rec.Body.String())
	}

	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("answer is not JSON: %v: %s", err, rec.Body.String())
	}

	if code, present := got["errorCode"]; present {
		t.Fatalf("errorCode = %v, want none: %s", code, rec.Body.String())
	}
}

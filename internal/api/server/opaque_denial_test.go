package server_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	apiserver "opendustpan.org/opendustpan/internal/api/server"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/identity"
)

// errOpaqueDenialFault is a failure that no class of the API maps.
var errOpaqueDenialFault = errors.New("the store did not answer")

// TestTokenRoutesDenyWithOneBody proves that the two token routes whose object
// comes from the stored token answer the same 403 whatever the stored token is.
func TestTokenRoutesDenyWithOneBody(t *testing.T) {
	t.Parallel()

	f := newAccessFixture(t)
	f.seedService(t, "deploy")

	var service, personal tokenDoc

	f.as(t, "admin", apiCall{
		http.MethodPost, "/api/server/principals/prn-deploy/tokens", `{"name":"ci"}`, http.StatusCreated,
	}, &service)
	f.as(t, "alice", apiCall{
		http.MethodPost, "/api/server/caller/tokens", `{"name":"laptop"}`, http.StatusCreated,
	}, &personal)

	type route struct {
		path   func(id string) string
		name   string
		method string
		body   string
	}

	routes := []route{
		{
			name: "delete token", method: http.MethodDelete, body: "",
			path: func(id string) string { return "/api/server/tokens/" + id },
		},
		{
			name: "rotate service token", method: http.MethodPost, body: "{}",
			path: func(id string) string { return "/api/server/service-tokens/" + id + "/rotate" },
		},
	}

	states := []struct {
		name string
		id   string
	}{
		{"existing service token", service.ID},
		{"existing personal token of another person", personal.ID},
		{"unknown well-formed ID", "tok-unknown"},
		{"malformed ID", "not-a-token"},
	}

	want := f.asToken(t, "bob", routes[0].method, routes[0].path(states[0].id), []byte(routes[0].body)).Body.String()

	for _, rt := range routes {
		for _, state := range states {
			t.Run(rt.name+"/"+state.name, func(t *testing.T) {
				t.Parallel()

				rec := f.asToken(t, "bob", rt.method, rt.path(state.id), []byte(rt.body))
				checkTokenDenial(t, rec, want, state.id)
			})
		}
	}
}

// checkTokenDenial fails unless rec is the one 403 body of a token route and
// the body names no action, no object kind and no token ID.
func checkTokenDenial(t *testing.T, rec *httptest.ResponseRecorder, want, id string) {
	t.Helper()

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (%s)", rec.Code, rec.Body.String())
	}

	if got := rec.Body.String(); got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}

	var body denialBody
	decodeInto(t, rec.Body.String(), &body)

	if body.ErrorCode != errcode.AccessDenied {
		t.Errorf("errorCode = %q, want %q", body.ErrorCode, errcode.AccessDenied)
	}

	for _, word := range []string{"principal", "server", "manage-tokens", id} {
		if strings.Contains(rec.Body.String(), word) {
			t.Errorf("body holds %q: %s", word, rec.Body.String())
		}
	}
}

// TestOpaqueDenialField proves that OpaqueDenial changes only the text of a
// refusal on its own route: a denial answers the plain text, the same denial
// without the field names the action and the kind, and any other error keeps
// its answer.
func TestOpaqueDenialField(t *testing.T) {
	t.Parallel()

	denied := &identity.DeniedError{
		Subject: "bob",
		Action:  identity.ActionPrincipalManageTokens,
		Object:  identity.PrincipalObject("prn-deploy"),
	}

	tests := []struct {
		authErr     error
		name        string
		wantCode    string
		wantMessage string
		wantStatus  int
		opaque      bool
	}{
		{
			name: "opaque denial", authErr: denied, opaque: true,
			wantStatus: http.StatusForbidden, wantCode: errcode.AccessDenied, wantMessage: "forbidden",
		},
		{
			name: "named denial", authErr: denied, opaque: false,
			wantStatus: http.StatusForbidden, wantCode: errcode.AccessDenied,
			wantMessage: "forbidden: you need the action principal:manage-tokens on this principal",
		},
		{
			name: "opaque other error", authErr: errOpaqueDenialFault, opaque: true,
			wantStatus: http.StatusInternalServerError, wantCode: errcode.ServerInternal, wantMessage: "internal error",
		},
		{
			name: "named other error", authErr: errOpaqueDenialFault, opaque: false,
			wantStatus: http.StatusInternalServerError, wantCode: errcode.ServerInternal, wantMessage: "internal error",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			handler := forbiddenHandler(t, tc.authErr)

			route := apiserver.Route{
				Method: http.MethodGet, Path: "/test/opaque-denial", Scope: apiserver.ScopeAuthorized,
				Actions: []identity.Action{identity.ActionPrincipalManageTokens},
				Resolve: func(_ http.ResponseWriter, _ *http.Request) (identity.Object, error) {
					return identity.PrincipalObject("prn-deploy"), nil
				},
				ActionsFor:   nil,
				OpaqueDenial: tc.opaque,
				Serve: func(w http.ResponseWriter, _ *http.Request, _ identity.Principal, _ identity.Object) {
					w.WriteHeader(http.StatusNoContent)
				},
			}

			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, route.Path, http.NoBody)
			req.Header.Set("Authorization", "Bearer admin-token")

			rec := httptest.NewRecorder()
			handler.ServeRouteForTest(rec, req, route)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d (%s)", rec.Code, tc.wantStatus, rec.Body.String())
			}

			var body denialBody
			decodeInto(t, rec.Body.String(), &body)

			if body.ErrorCode != tc.wantCode {
				t.Errorf("errorCode = %q, want %q", body.ErrorCode, tc.wantCode)
			}

			if body.Message != tc.wantMessage {
				t.Errorf("message = %q, want %q", body.Message, tc.wantMessage)
			}
		})
	}
}

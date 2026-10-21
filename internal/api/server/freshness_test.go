package server_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/maintenance"
)

// freshErrorCode is the errorCode of a change that has no fresh authentication.
const freshErrorCode = "mfa.fresh-step-required"

// freshRoute is a route that needs a fresh authentication. setup runs once
// before the requests and returns the path to use, or "" for path. want is
// the status of a console session with a recent sign-in.
type freshRoute struct {
	setup  func(t *testing.T, e providerEnv) string
	name   string
	method string
	path   string
	body   string
	want   int
}

// freshRoutes lists each route that needs a fresh authentication today.
func freshRoutes() []freshRoute {
	principalTokens := "/api/server/principals/" + testOtherPrincipalID + "/tokens"
	createBeta := func(t *testing.T, e providerEnv) string {
		t.Helper()

		e.createBeta(t)

		return ""
	}
	// The person of the sessions is not the caller of the access token, so the
	// own token to rotate is one that a fresh session makes.
	ownToken := func(t *testing.T, e providerEnv) string {
		t.Helper()

		rec := e.postWithSession(t, e.freshSignIn(t), "/api/server/caller/tokens", ownTokenBody())
		if rec.Code != http.StatusCreated {
			t.Fatalf("make the own token = %d %s, want 201", rec.Code, rec.Body.String())
		}

		var made tokenDoc
		decodeInto(t, rec.Body.String(), &made)

		return "/api/server/caller/tokens/" + made.ID + "/rotate"
	}
	seedDefault := func(t *testing.T, e providerEnv) string {
		t.Helper()

		rec := e.sendWithSession(
			t,
			http.MethodPut,
			e.freshSignIn(t),
			gateBase+testNamespace+gateDefault,
			platformDefault,
		)
		if rec.Code != http.StatusOK {
			t.Fatalf("seed the approval default = %d %s, want 200", rec.Code, rec.Body.String())
		}

		return ""
	}
	seedRule := func(t *testing.T, e providerEnv) string {
		t.Helper()

		rec := e.sendWithSession(t, http.MethodPut, e.freshSignIn(t), gateBase+testNamespace+gateRulesURL+"/ops",
			`{"count":1,"approvers":{"groups":["ops"]}}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("seed the approval rule = %d %s, want 200", rec.Code, rec.Body.String())
		}

		return ""
	}

	return []freshRoute{
		{nil, "create own token", http.MethodPost, "/api/server/caller/tokens", ownTokenBody(), http.StatusCreated},
		{
			ownToken, "rotate own token", http.MethodPost, "/api/server/caller/tokens/{id}/rotate", `{}`,
			http.StatusCreated,
		},
		{
			nil, "create service token", http.MethodPost, "/api/server/service-tokens",
			`{"principalID":"` + testOtherPrincipalID + `","name":"ci"}`, http.StatusCreated,
		},
		{
			nil,
			"rotate service token",
			http.MethodPost,
			"/api/server/service-tokens/" + testServiceTokenID + "/rotate",
			`{}`,
			http.StatusCreated,
		},
		{nil, "create principal token", http.MethodPost, principalTokens, `{"name":"ci"}`, http.StatusCreated},
		{
			nil,
			"rotate principal token",
			http.MethodPost,
			principalTokens + "/" + testServiceTokenID + "/rotate",
			`{}`,
			http.StatusCreated,
		},
		{
			nil, "change session policy", http.MethodPatch, "/api/server/session-policy", `{"idleSeconds":1800}`,
			http.StatusOK,
		},
		{nil, "create provider", http.MethodPost, "/api/server/sign-in-providers", "", http.StatusCreated},
		{
			createBeta, "change provider", http.MethodPatch, "/api/server/sign-in-providers/beta",
			`{"version":1,"enabled":false}`, http.StatusOK,
		},
		{
			createBeta, "delete provider", http.MethodDelete, "/api/server/sign-in-providers/beta", "",
			http.StatusNoContent,
		},
		{
			nil, "set approval default", http.MethodPut, gateBase + testNamespace + gateDefault, platformDefault,
			http.StatusOK,
		},
		{
			seedDefault, "delete approval default", http.MethodDelete, gateBase + testNamespace + gateDefault, "",
			http.StatusNoContent,
		},
		{
			nil, "set approval rule", http.MethodPut, gateBase + testNamespace + gateRulesURL + "/ops",
			`{"count":1,"approvers":{"groups":["ops"]}}`, http.StatusOK,
		},
		{
			seedRule, "delete approval rule", http.MethodDelete, gateBase + testNamespace + gateRulesURL + "/ops", "",
			http.StatusNoContent,
		},
	}
}

// errorBody is the part of an error answer that these tests read.
type errorBody struct {
	Message        string `json:"message"`
	ErrorCode      string `json:"errorCode"`
	Reauthenticate string `json:"reauthenticate"`
	Code           int    `json:"code"`
}

func decodeErrorBody(t *testing.T, rec *httptest.ResponseRecorder) errorBody {
	t.Helper()

	var body errorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode the error %q: %v", rec.Body.String(), err)
	}

	return body
}

// TestFreshAuthRoutes proves for each route that needs a fresh
// authentication: an access token is refused with 403 and mfa.fresh-step-required,
// a console session with an old sign-in is refused with the path of a new
// sign-in and the same code, and a console session with a recent sign-in
// is allowed.
func TestFreshAuthRoutes(t *testing.T) {
	t.Parallel()

	for _, route := range freshRoutes() {
		t.Run(route.name, func(t *testing.T) {
			t.Parallel()
			checkFreshAuthRoute(t, route)
		})
	}
}

// checkFreshAuthRoute sends route with an access token, a console session
// with an old sign-in and a console session with a recent sign-in.
func checkFreshAuthRoute(t *testing.T, route freshRoute) {
	t.Helper()

	e := newProviderEnv(t)

	body, path := route.body, route.path
	if route.name == "create provider" {
		body = e.betaBody()
	}

	if route.setup != nil {
		if setPath := route.setup(t, e); setPath != "" {
			path = setPath
		}
	}

	checkTokenRefused(t, e, route, path, body)

	rec := e.sendWithSession(t, route.method, e.staleSession(t), path, body)

	got := decodeErrorBody(t, rec)
	if rec.Code != http.StatusUnauthorized || got.ErrorCode != freshErrorCode ||
		!strings.HasPrefix(got.Reauthenticate, "/api/server/sign-in/corp?fresh=1") {
		t.Errorf("stale session = %d %s, want 401 with errorCode %s and a path of a new sign-in", rec.Code,
			rec.Body.String(), freshErrorCode)
	}

	rec = e.sendWithSession(t, route.method, e.freshSignIn(t), path, body)
	if rec.Code != route.want {
		t.Errorf("fresh session = %d %s, want %d", rec.Code, rec.Body.String(), route.want)
	}
}

// checkTokenRefused proves that an access token never gives a fresh
// authentication: the route answers 403 with mfa.fresh-step-required.
func checkTokenRefused(t *testing.T, e providerEnv, route freshRoute, path, body string) {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), route.method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-token")

	rec := httptest.NewRecorder()
	e.handler.ServeHTTP(rec, req)

	got := decodeErrorBody(t, rec)
	if rec.Code != http.StatusForbidden || got.ErrorCode != freshErrorCode || got.Code != http.StatusForbidden {
		t.Errorf("access token = %d %s, want 403 with errorCode %s", rec.Code, rec.Body.String(), freshErrorCode)
	}
}

// TestFreshAuthTokenCreateInMaintenanceMode proves that a token change with
// a fresh session still answers 503 in maintenance mode: the fresh check
// does not replace the maintenance check. The access token case is in
// TestFreshAuthRoutes.
func TestFreshAuthTokenCreateInMaintenanceMode(t *testing.T) {
	t.Parallel()

	e := newSignInEnv(t, true)
	sess := e.freshSignIn(t)

	if err := e.store.SetState(t.Context(), maintenance.StateMaintenance); err != nil {
		t.Fatalf("enter maintenance mode: %v", err)
	}

	rec := e.postWithSession(t, sess, "/api/server/principals/"+testOtherPrincipalID+"/tokens", `{"name":"ci"}`)
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "maintenance") {
		t.Fatalf("token create in maintenance mode = %d %s, want 503 for maintenance mode", rec.Code, rec.Body.String())
	}
}

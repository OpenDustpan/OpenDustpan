package server_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	apiserver "opendustpan.org/opendustpan/internal/api/server"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/session"
)

// freshErrorCode is the errorCode of a change that has no fresh authentication.
const freshErrorCode = "mfa.fresh-step-required"

// errPolicyStoreDown is the error of a policy store that cannot answer.
var errPolicyStoreDown = errors.New("the policy store is down")

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

		rec := e.postWithSession(t, e.freshSignIn(t), "/api/dustpan/v1/me/tokens", ownTokenBody())
		if rec.Code != http.StatusCreated {
			t.Fatalf("make the own token = %d %s, want 201", rec.Code, rec.Body.String())
		}

		var made tokenDoc
		decodeInto(t, rec.Body.String(), &made)

		return "/api/dustpan/v1/me/tokens/" + made.ID + "/rotate"
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

	administrator := func(t *testing.T, e providerEnv) string {
		t.Helper()

		e.makeSiteAdministrator(t)

		return ""
	}
	makeRole := func(t *testing.T, e providerEnv) string {
		t.Helper()

		e.makeSiteAdministrator(t)

		rec := e.postWithSession(
			t,
			e.freshSignIn(t),
			"/api/server/roles",
			`{"name":"deployer","actions":["unit:read"]}`,
		)
		if rec.Code != http.StatusCreated {
			t.Fatalf("make the role = %d %s, want 201", rec.Code, rec.Body.String())
		}

		var made roleDoc
		decodeInto(t, rec.Body.String(), &made)

		return "/api/server/roles/" + made.ID
	}
	seedAllowlist := func(t *testing.T, e providerEnv) string {
		t.Helper()

		rec := e.sendWithSession(t, http.MethodPut, e.freshSignIn(t), ipAllowlistTarget, ipAllowlistBody)
		if rec.Code != http.StatusOK {
			t.Fatalf("seed the allowlist = %d %s, want 200", rec.Code, rec.Body.String())
		}

		return ""
	}
	grantBody := `{"subject":"principal:` + testPrincipalID + `","role":"viewer"}`
	return slices.Concat(
		freshGroupRoutes(makeFreshGroup, makeFreshGroupWithMember),
		freshOwnRoutes(createBeta, ownToken, seedDefault, seedRule, principalTokens),
		freshAccessRoutes(administrator, makeRole, grantBody),
		freshOtherRoutes(seedAllowlist),
	)
}

// makeFreshGroup is the setup of a group route: it makes the person of the
// session a site administrator and makes a group. A group grant applies to
// each member, so a membership change is an act-as edge. The setup returns the
// path of the group.
func makeFreshGroup(t *testing.T, e providerEnv) string {
	t.Helper()

	e.makeSiteAdministrator(t)

	var made groupDoc

	rec := e.postWithSession(t, e.freshSignIn(t), "/api/server/groups", `{"name":"releasers"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("make the group = %d %s, want 201", rec.Code, rec.Body.String())
	}

	decodeInto(t, rec.Body.String(), &made)

	return "/api/server/groups/" + made.ID
}

// makeFreshGroupWithMember is makeFreshGroup with a member in the group.
func makeFreshGroupWithMember(t *testing.T, e providerEnv) string {
	t.Helper()

	path := makeFreshGroup(t, e)

	rec := e.postWithSession(t, e.freshSignIn(t), path+"/members", `{"principalID":"`+testOtherPrincipalID+`"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("make the member = %d %s, want 201", rec.Code, rec.Body.String())
	}

	return path
}

// freshGroupRoutes lists the writes of group membership that need a fresh
// authentication. The setup of each route returns the path of its group. A
// route with a member path has the member in setupMember.
func freshGroupRoutes(setupGroup, setupMember func(*testing.T, providerEnv) string) []freshRoute {
	return []freshRoute{
		{
			func(t *testing.T, e providerEnv) string {
				t.Helper()

				return setupGroup(t, e) + "/members"
			},
			"add group member", http.MethodPost, "", `{"principalID":"` + testOtherPrincipalID + `"}`,
			http.StatusCreated,
		},
		{
			func(t *testing.T, e providerEnv) string {
				t.Helper()

				return setupMember(t, e) + "/members/" + testOtherPrincipalID
			},
			"remove group member", http.MethodDelete, "", "", http.StatusNoContent,
		},
		{setupGroup, "delete group", http.MethodDelete, "", "", http.StatusNoContent},
	}
}

// ipAllowlistTarget and ipAllowlistBody are the route and the body of the
// allowlist writes of the fresh authentication tests.
const (
	ipAllowlistTarget = "/api/server/namespaces/" + testNamespace + "/ip-allowlist"
	ipAllowlistBody   = `{"cidrs":["10.0.0.0/8"]}`
)

// freshAccessRoutes lists the writes of roles and grants that need a fresh
// authentication. administrator makes the person of the session a site
// administrator, and makeRole also makes a role and returns its path.
func freshAccessRoutes(administrator, makeRole func(*testing.T, providerEnv) string, grantBody string) []freshRoute {
	return []freshRoute{
		{
			administrator, "create role", http.MethodPost, "/api/server/roles",
			`{"name":"deployer","actions":["unit:read"]}`, http.StatusCreated,
		},
		{makeRole, "change role", http.MethodPatch, "", `{"description":"reads units"}`, http.StatusOK},
		{
			administrator, "grant on the installation", http.MethodPost, "/api/server/grants",
			`{"subject":"principal:` + testPrincipalID + `","role":"installation-operator"}`, http.StatusCreated,
		},
		{
			administrator,
			"grant on a namespace",
			http.MethodPost,
			"/api/server/namespaces/" + testNamespace + "/grants",
			grantBody,
			http.StatusCreated,
		},
		{
			administrator, "grant on a collection", http.MethodPost,
			"/api/server/namespaces/" + testNamespace + "/collections/" + testCollection + "/grants", grantBody,
			http.StatusCreated,
		},
		{
			administrator, "grant on a principal", http.MethodPost,
			"/api/server/principals/" + testOtherPrincipalID + "/grants",
			`{"subject":"principal:` + testPrincipalID + `","role":"token-manager"}`, http.StatusCreated,
		},
	}
}

// freshOtherRoutes lists the writes of the token policy, the audit chain and
// the IP allowlists that need a fresh authentication. seedAllowlist stores a
// list first.
func freshOtherRoutes(seedAllowlist func(*testing.T, providerEnv) string) []freshRoute {
	return []freshRoute{
		{
			nil,
			"change token policy",
			http.MethodPatch,
			"/api/server/token-policy",
			`{"inactivitySeconds":0}`,
			http.StatusOK,
		},
		{nil, "approve audit chain", http.MethodPost, "/api/dustpan/v1/audit/chain", "", http.StatusAccepted},
		{nil, "set IP allowlist", http.MethodPut, ipAllowlistTarget, ipAllowlistBody, http.StatusOK},
		{seedAllowlist, "remove IP allowlist", http.MethodDelete, ipAllowlistTarget, "", http.StatusNoContent},
	}
}

// freshOwnRoutes lists the token routes, the session policy, the providers
// and the gate writes that need a fresh authentication.
func freshOwnRoutes(
	createBeta, ownToken, seedDefault, seedRule func(*testing.T, providerEnv) string, principalTokens string,
) []freshRoute {
	return []freshRoute{
		{nil, "create own token", http.MethodPost, "/api/dustpan/v1/me/tokens", ownTokenBody(), http.StatusCreated},
		{
			ownToken, "rotate own token", http.MethodPost, "/api/dustpan/v1/me/tokens/{id}/rotate", `{}`,
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

	checkTokenRefused(t, e.handler, route.method, path, body)

	rec := e.sendWithSession(t, route.method, e.staleSession(t), path, body)

	got := decodeErrorBody(t, rec)
	if rec.Code != http.StatusForbidden || got.ErrorCode != freshErrorCode ||
		!strings.HasPrefix(got.Reauthenticate, "/api/server/sign-in/corp?fresh=1") {
		t.Errorf("stale session = %d %s, want 403 with errorCode %s and a path of a new sign-in", rec.Code,
			rec.Body.String(), freshErrorCode)
	}

	rec = e.sendWithSession(t, route.method, e.freshSignIn(t), path, body)
	if rec.Code != route.want {
		t.Errorf("fresh session = %d %s, want %d", rec.Code, rec.Body.String(), route.want)
	}
}

// checkTokenRefused proves that an access token never gives a fresh
// authentication: the route answers 403 with mfa.fresh-step-required.
func checkTokenRefused(t *testing.T, h testHandler, method, path, body string) {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-token")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	got := decodeErrorBody(t, rec)
	if rec.Code != http.StatusForbidden || got.ErrorCode != freshErrorCode || got.Code != http.StatusForbidden {
		t.Errorf("access token = %d %s, want 403 with errorCode %s", rec.Code, rec.Body.String(), freshErrorCode)
	}
}

// hostFreshRoute is a Git host write that needs a fresh authentication. setup
// runs once before the requests and makes the object that the write needs.
type hostFreshRoute struct {
	setup  func(t *testing.T, e *hostEnv)
	name   string
	method string
	target string
	body   string
	want   int
}

// hostFreshRoutes lists the Git host writes that need a fresh authentication:
// the writes of the registration of the app and of the map of installations.
// The writes of the connections and of the account links need no fresh
// authentication, so they are not here.
func hostFreshRoutes() []hostFreshRoute {
	registerApp := func(t *testing.T, e *hostEnv) {
		t.Helper()

		wantHost(t, e.send(t, http.MethodPut, hostAppPath, hostAppBody()), http.StatusCreated)
	}
	assignInstallation := func(t *testing.T, e *hostEnv) {
		t.Helper()

		wantHost(t, e.send(t, http.MethodPut, hostInstallationsPath+"/101", `{"namespace":"acme"}`), http.StatusCreated)
	}

	return []hostFreshRoute{
		{nil, "register the app", http.MethodPut, hostAppPath, hostAppBody(), http.StatusCreated},
		{registerApp, "delete the app", http.MethodDelete, hostAppPath, "", http.StatusNoContent},
		{
			nil, "assign an installation", http.MethodPut, hostInstallationsPath + "/102", `{"namespace":"acme"}`,
			http.StatusCreated,
		},
		{
			assignInstallation, "unassign an installation", http.MethodDelete, hostInstallationsPath + "/101", "",
			http.StatusNoContent,
		},
	}
}

// hostStaleSignInAge is the age of a sign-in that is older than the token
// freshness window of the Git host handler.
const hostStaleSignInAge = session.DefaultTokenFreshness + time.Minute

// TestFreshAuthGitHostRoutes proves for each Git host write that needs a fresh
// authentication: an access token is refused with 403 and
// mfa.fresh-step-required, a console session with an old sign-in is refused
// with the path of a new sign-in and the same code, and a console session with
// a recent sign-in is allowed.
func TestFreshAuthGitHostRoutes(t *testing.T) {
	t.Parallel()

	for _, route := range hostFreshRoutes() {
		t.Run(route.name, func(t *testing.T) {
			t.Parallel()

			e := newHostEnv(t, hostAllowAll())

			if route.setup != nil {
				route.setup(t, e)
			}

			checkTokenRefused(t, e.handler, route.method, route.target, route.body)

			rec := sendHostSession(t, e, route, hostStaleSignInAge)

			got := decodeErrorBody(t, rec)
			if rec.Code != http.StatusForbidden || got.ErrorCode != freshErrorCode ||
				!strings.HasPrefix(got.Reauthenticate, "/api/server/sign-in/corp?fresh=1") {
				t.Errorf("stale session = %d %s, want 403 with errorCode %s and a path of a new sign-in", rec.Code,
					rec.Body.String(), freshErrorCode)
			}

			rec = sendHostSession(t, e, route, 0)
			if rec.Code != route.want {
				t.Errorf("fresh session = %d %s, want %d", rec.Code, rec.Body.String(), route.want)
			}
		})
	}
}

// sendHostSession sends a Git host request as a console session whose sign-in
// is age old.
func sendHostSession(
	t *testing.T, e *hostEnv, route hostFreshRoute, age time.Duration,
) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), route.method, route.target, strings.NewReader(route.body))
	req.RemoteAddr = hostRemote
	startSession(t, e.handler, req, age)

	rec := httptest.NewRecorder()
	e.handler.ServeHTTP(rec, req)

	return rec
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

// TestFreshAuthRefusesASignInAfterNow proves a session whose sign-in is after
// the clock, for example after the clock steps back, is refused like a session
// with an old sign-in: its age is negative, and no window accepts it.
func TestFreshAuthRefusesASignInAfterNow(t *testing.T) {
	t.Parallel()

	e := newProviderEnv(t)
	sess := e.freshSignIn(t)

	rec := e.sendWithSession(t, http.MethodPatch, sess, "/api/server/session-policy", `{"idleSeconds":1800}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("change from a fresh session = %d %s, want 200", rec.Code, rec.Body.String())
	}

	e.clock.Add(-time.Minute)

	rec = e.sendWithSession(t, http.MethodPatch, sess, "/api/server/session-policy", `{"idleSeconds":900}`)

	got := decodeErrorBody(t, rec)
	if rec.Code != http.StatusForbidden || got.ErrorCode != freshErrorCode ||
		!strings.HasPrefix(got.Reauthenticate, "/api/server/sign-in/corp?fresh=1") {
		t.Fatalf("change from a sign-in after now = %d %s, want 403 with the path of a new sign-in", rec.Code,
			rec.Body.String())
	}
}

// failureEntries counts the audit entries of the scope with the outcome
// failure and the action.
func failureEntries(t *testing.T, e providerEnv, scope namespace.Name, action identity.Action) int {
	t.Helper()

	listing, err := e.store.List(t.Context(), scope, audit.Page{Number: 1, Size: audit.MaxPageSize})
	if err != nil {
		t.Fatalf("list audit of %s: %v", scope, err)
	}

	count := 0

	for i := range listing.Entries {
		if entry := &listing.Entries[i]; entry.Outcome == audit.OutcomeFailure && entry.Action == string(action) {
			count++
		}
	}

	return count
}

// TestFreshAuthRefusalWritesAFailureEntry proves a gate write and a role
// write that the fresh check refuses leave one failure entry each: for an
// access token and for a session with an old sign-in.
func TestFreshAuthRefusalWritesAFailureEntry(t *testing.T) {
	t.Parallel()

	gateWrite := freshWrite{
		method: http.MethodPut, path: gateBase + testNamespace + gateDefault, body: platformDefault,
		scope: testNamespace, action: identity.ActionNamespaceManageGates,
	}
	roleWrite := freshWrite{
		method: http.MethodPost, path: "/api/server/roles", body: `{"name":"deployer","actions":["unit:read"]}`,
		scope: audit.ServerScope, action: identity.ActionServerManageRoles,
	}

	writes := []freshWrite{gateWrite, roleWrite}

	for i := range writes {
		w := &writes[i]

		t.Run(string(w.action), func(t *testing.T) {
			t.Parallel()

			e := newProviderEnv(t)

			req := httptest.NewRequestWithContext(t.Context(), w.method, w.path, strings.NewReader(w.body))
			req.Header.Set("Authorization", "Bearer test-token")

			rec := httptest.NewRecorder()
			e.handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusForbidden {
				t.Fatalf("access token = %d %s, want 403", rec.Code, rec.Body.String())
			}

			if got := failureEntries(t, e, w.scope, w.action); got != 1 {
				t.Fatalf("failure entries after the access token = %d, want 1", got)
			}

			rec = e.sendWithSession(t, w.method, e.staleSession(t), w.path, w.body)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("stale session = %d %s, want 403", rec.Code, rec.Body.String())
			}

			if got := failureEntries(t, e, w.scope, w.action); got != 2 {
				t.Fatalf("failure entries after the stale session = %d, want 2", got)
			}
		})
	}
}

// freshWrite is a write that needs a fresh authentication, with the scope and
// the action of the audit entry of its refusal.
type freshWrite struct {
	method string
	path   string
	body   string
	scope  namespace.Name
	action identity.Action
}

// TestFreshWindowZeroAsksEachTime proves that a window of 0 asks for a fresh
// authentication at each guarded step: the create of an own token refuses a
// session whose sign-in is fresh, and refuses a session with an old sign-in,
// each with 403, the code and the path of a new sign-in.
func TestFreshWindowZeroAsksEachTime(t *testing.T) {
	t.Parallel()

	e := newProviderEnv(t)
	sess := e.freshSignIn(t)

	_, err := e.store.UpdateSessionPolicy(t.Context(), func(old session.Policy) (session.Policy, audit.Entry, error) {
		old.TokenFreshness, old.TokenFreshnessDefault, old.UpdatedAt = 0, false, e.clock.Now()

		return old, policyChangeEntry(e.clock.Now()), nil
	})
	if err != nil {
		t.Fatalf("set the window to 0: %v", err)
	}

	// The step at age 0: the sign-in of the session is fresh, and the window
	// of 0 refuses it.
	rec := e.postWithSession(t, sess, "/api/dustpan/v1/me/tokens", ownTokenBody())
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), freshErrorCode) ||
		!strings.Contains(rec.Body.String(), "/api/server/sign-in/corp?fresh=1") {
		t.Fatalf("own token at age 0 = %d %s, want 403 with the code and the path of a new sign-in",
			rec.Code, rec.Body.String())
	}

	// The step with an old sign-in: the session stays active, and the refusal
	// is the same.
	stale := e.staleSession(t)

	rec = e.postWithSession(t, stale, "/api/dustpan/v1/me/tokens", ownTokenBody())
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), freshErrorCode) ||
		!strings.Contains(rec.Body.String(), "/api/server/sign-in/corp?fresh=1") {
		t.Fatalf("own token with an old sign-in = %d %s, want 403 with the code and the path of a new sign-in",
			rec.Code, rec.Body.String())
	}
}

// TestStaleErrorNamesSignInPath proves that the 403 of a step with no fresh
// authentication holds the code mfa.fresh-step-required and the path of a new
// sign-in: a valid session with a stale sign-in, and a valid session under a
// window of 0, each get it. Each refused change stores no token.
func TestStaleErrorNamesSignInPath(t *testing.T) {
	t.Parallel()

	e := newSignInEnv(t, true)

	// A valid session whose sign-in is older than the window.
	checkStaleNamesSignInPath(t, e, e.staleSession(t))

	// A valid session whose sign-in is fresh, under a window of 0.
	fresh := e.signIn(t)

	_, err := e.store.UpdateSessionPolicy(t.Context(), func(old session.Policy) (session.Policy, audit.Entry, error) {
		old.TokenFreshness, old.TokenFreshnessDefault, old.UpdatedAt = 0, false, e.clock.Now()

		return old, policyChangeEntry(e.clock.Now()), nil
	})
	if err != nil {
		t.Fatalf("set the window to 0: %v", err)
	}

	checkStaleNamesSignInPath(t, e, fresh)
}

// checkStaleNamesSignInPath sends the create of an own token with the session
// cookie and fails unless the answer is 403 with the code
// mfa.fresh-step-required and the path of a new sign-in, and the refused
// change stores no token.
func checkStaleNamesSignInPath(t *testing.T, e signInEnv, sess *http.Cookie) {
	t.Helper()

	const reauthenticate = "/api/server/sign-in/corp?fresh=1"

	before := e.sendWithSession(t, http.MethodGet, sess, "/api/dustpan/v1/me/tokens", "")
	if before.Code != http.StatusOK {
		t.Fatalf("list the tokens = %d %s, want 200", before.Code, before.Body.String())
	}

	rec := e.postWithSession(t, sess, "/api/dustpan/v1/me/tokens", ownTokenBody())

	got := decodeErrorBody(t, rec)
	if rec.Code != http.StatusForbidden || got.ErrorCode != freshErrorCode ||
		got.Reauthenticate != reauthenticate {
		t.Fatalf("refusal = %d %s, want 403 with errorCode %s and reauthenticate %q", rec.Code,
			rec.Body.String(), freshErrorCode, reauthenticate)
	}

	after := e.sendWithSession(t, http.MethodGet, sess, "/api/dustpan/v1/me/tokens", "")
	if after.Code != http.StatusOK || after.Body.String() != before.Body.String() {
		t.Fatalf("tokens after the refusal = %d %s, want the list from before: %s", after.Code,
			after.Body.String(), before.Body.String())
	}
}

// TestFreshAuthRefusesAForgedFreshTimeHeader proves that no header can make
// a stale session fresh: the fresh time comes only from the sealed session
// cookie, so a request with a stale session and a header that claims a fresh
// sign-in still gets 403 with the code and the path of a new sign-in.
func TestFreshAuthRefusesAForgedFreshTimeHeader(t *testing.T) {
	t.Parallel()

	e := newSignInEnv(t, true)
	sess := e.staleSession(t)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/dustpan/v1/me/tokens",
		strings.NewReader(ownTokenBody()))
	req.AddCookie(sess)

	_, s := e.readSession(t, sess)
	req.Header.Set(apiserver.CSRFHeader, s.CSRFToken)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	// The header claims a sign-in now. The server reads no fresh time from a
	// header: it ignores this one, like each other header.
	req.Header.Set("X-Dustpan-Auth-Time", e.clock.Now().UTC().Format(time.RFC3339))

	rec := httptest.NewRecorder()
	e.handler.ServeHTTP(rec, req)

	got := decodeErrorBody(t, rec)
	if rec.Code != http.StatusForbidden || got.ErrorCode != freshErrorCode ||
		got.Reauthenticate != "/api/server/sign-in/corp?fresh=1" {
		t.Fatalf("stale session with a forged fresh-time header = %d %s, want 403 with the code and the path",
			rec.Code, rec.Body.String())
	}
}

// TestFreshAuthRefusesAnOutOfRangeStoredWindow proves that a stored window
// outside 0 and 60 to 900 seconds cannot extend the window or turn the check
// off: the step refuses the session like a window of 0 does, with 403 and the
// path of a new sign-in.
func TestFreshAuthRefusesAnOutOfRangeStoredWindow(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		d    time.Duration
	}{
		{"30 seconds", 30 * time.Second},
		{"59 seconds", 59 * time.Second},
		{"901 seconds", 901 * time.Second},
		{"1000 seconds", 1000 * time.Second},
		{"100000 seconds", 100000 * time.Second},
		{"negative", -time.Second},
		{"fraction of a second", 61*time.Second + time.Millisecond},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			e := newSignInEnv(t, true)
			sess := e.signIn(t)

			_, err := e.store.UpdateSessionPolicy(t.Context(),
				func(old session.Policy) (session.Policy, audit.Entry, error) {
					old.TokenFreshness, old.TokenFreshnessDefault, old.UpdatedAt = tc.d, false, e.clock.Now()

					return old, policyChangeEntry(e.clock.Now()), nil
				})
			if err != nil {
				t.Fatalf("store an out-of-range window: %v", err)
			}

			rec := e.postWithSession(t, sess, "/api/dustpan/v1/me/tokens", ownTokenBody())

			got := decodeErrorBody(t, rec)
			if rec.Code != http.StatusForbidden || got.ErrorCode != freshErrorCode ||
				got.Reauthenticate != "/api/server/sign-in/corp?fresh=1" {
				t.Fatalf("own token with window %s = %d %s, want 403 with the code and the path",
					tc.d, rec.Code, rec.Body.String())
			}
		})
	}
}

// TestFreshAuthFailsClosedWhenThePolicyReadFails proves that a store that
// cannot read the session policy refuses a guarded step: the server fails
// closed with the 403 of a step with no fresh authentication, and the refusal
// names the path of a new sign-in.
func TestFreshAuthFailsClosedWhenThePolicyReadFails(t *testing.T) {
	t.Parallel()

	// The first reads answer the sign-in and the read of the session of the
	// harness; each later read fails, as a store that is down does.
	var down atomic.Bool

	e := newSignInEnvWith(t, signInEnvOptions{
		autoCreate: true, noPolicyRoutes: false, noGateEngine: false, stored: nil, authz: nil, namespaces: nil,
		hostile: nil, passwords: nil, idempotency: false,
		failPolicyRead: func() error {
			if down.Load() {
				return errPolicyStoreDown
			}

			return nil
		},
	})

	sess := e.signIn(t)

	_, s := e.readSession(t, sess)
	down.Store(true)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/dustpan/v1/me/tokens",
		strings.NewReader(ownTokenBody()))
	req.AddCookie(sess)
	req.Header.Set(apiserver.CSRFHeader, s.CSRFToken)
	req.Header.Set("Sec-Fetch-Site", "same-origin")

	rec := httptest.NewRecorder()
	e.handler.ServeHTTP(rec, req)

	got := decodeErrorBody(t, rec)
	if rec.Code != http.StatusForbidden || got.ErrorCode != freshErrorCode ||
		got.Reauthenticate != "/api/server/sign-in/corp?fresh=1" {
		t.Fatalf("own token with a failed policy read = %d %s, want 403 with the code and the path",
			rec.Code, rec.Body.String())
	}
}
